/*
Copyright 2026 The Perkeep Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"perkeep.org/internal/osutil"
	search "perkeep.org/internal/searchtypes"
	"perkeep.org/internal/takeout"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/cmdmain"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/upload"
)

type takeoutCmd struct {
	dir         string
	driveFolder string
	all         bool
	workers     int
	permanodes  bool
	verbose     bool
}

func init() {
	cmdmain.RegisterMode("takeout", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(takeoutCmd)
		flags.StringVar(&cmd.dir, "dir", "", "Read the exports' zip files from this local directory, rather than from Google Drive.")
		flags.StringVar(&cmd.driveFolder, "drive-folder", "", "The ID or URL of the Google Drive folder with the exports' zip files. If empty, the folders named Takeout in the root of Google Drive.")
		flags.BoolVar(&cmd.all, "all", false, "Consider all of the exports' files, not just Google Photos' photos and videos.")
		flags.IntVar(&cmd.workers, "j", 8, "How many files to check or upload at once.")
		flags.BoolVar(&cmd.permanodes, "permanodes", true, "Make a permanode for each uploaded file, based on its contents, as pk-put -filenodes does.")
		flags.BoolVar(&cmd.verbose, "v", false, "List the missing or uploaded files.")
		return cmd
	})
}

func (c *takeoutCmd) Describe() string {
	return "Upload the Google Photos files of Google Takeout exports that the server doesn't have."
}

func (c *takeoutCmd) Usage() {
	fmt.Fprintf(os.Stderr, `Usage: pk [globalopts] takeout [takeoutopts] list
       pk [globalopts] takeout [takeoutopts] stats <export>
       pk [globalopts] takeout [takeoutopts] upload <export>

Reads Google Takeout exports, from Google Drive or from a local directory
of their zip files (-dir), and finds which of their Google Photos files the
server has, by their sizes and CRC-32s, which the zip files record and the
server indexes. "stats" reports them; "upload" uploads the rest, reading
only those files' parts of the zip files. An export is named by its zip
files' common prefix, such as takeout-20240101T120000Z.

To read Google Drive, put a Google OAuth client ID for a desktop app, as
downloaded from the Google Cloud console, in %s.
The first run opens a browser to authorize reading Google Drive.
`, filepath.Join(takeoutConfigDir(), "takeout-oauth-client.json"))
}

func (c *takeoutCmd) Examples() []string {
	return []string{
		"list",
		"stats takeout-20240101T120000Z",
		"-dir=$HOME/Downloads upload takeout-20240101T120000Z",
	}
}

func (c *takeoutCmd) RunCommand(args []string) error {
	if len(args) == 0 {
		return cmdmain.UsageError("missing subcommand")
	}
	mode, args := args[0], args[1:]
	switch mode {
	case "list":
		if len(args) != 0 {
			return cmdmain.UsageError("list takes no arguments")
		}
	case "stats", "upload":
		if len(args) != 1 {
			return cmdmain.UsageError(mode + " takes the name of an export")
		}
	default:
		return cmdmain.UsageError("unknown subcommand " + mode)
	}
	ctx := context.Background()

	exps, err := c.exports(ctx)
	if err != nil {
		return err
	}
	if mode == "list" {
		if len(exps) == 0 {
			return errors.New("no Takeout exports found")
		}
		for _, e := range exps {
			var size int64
			for _, z := range e.Zips {
				size += z.Size
			}
			fmt.Fprintf(cmdmain.Stdout, "%s\t%d zip files\t%s\n", e.Name, len(e.Zips), takeoutBytes(size))
		}
		return nil
	}
	exp, err := takeout.FindExport(exps, args[0])
	if err != nil {
		return err
	}

	cl, err := client.New()
	if err != nil {
		return err
	}
	st := &takeoutStore{c: cl, permanodes: c.permanodes && mode == "upload", lim: upload.NewLimiter(upload.Limits{})}
	if err := st.checkServer(ctx); err != nil {
		return err
	}
	if st.permanodes {
		if st.signer, err = cl.Signer(); err != nil {
			return fmt.Errorf("making permanodes needs a signing identity (or use -permanodes=false): %w", err)
		}
		if err := cl.UploadPublicKey(ctx); err != nil {
			return err
		}
	}

	log.Printf("Reading the tables of contents of %d zip files...", len(exp.Zips))
	if err := exp.LoadTOCs(ctx, c.workers); err != nil {
		return err
	}
	var ents []takeout.Entry
	for _, e := range exp.Entries() {
		if c.all || takeout.IsPhotosMedia(e.File.Name) {
			ents = append(ents, e)
		}
	}
	cs := takeout.Contents(ents)
	log.Printf("Asking the server about %d distinct contents of %d files...", len(cs), len(ents))
	pr := newTakeoutProgress(len(cs))
	have, missing, err := takeout.Check(ctx, st, cs, c.workers, pr.update)
	if err != nil {
		return err
	}

	out := cmdmain.Stdout
	fmt.Fprintf(out, "Export %s: %d zip files\n", exp.Name, len(exp.Zips))
	what := "Google Photos files"
	if c.all {
		what = "files"
	}
	fmt.Fprintf(out, "  %d %s, %d distinct contents, %s\n", len(ents), what, len(cs), takeoutBytes(contentsSize(cs)))
	fmt.Fprintf(out, "  server has %d (%s); missing %d (%s)\n", len(have), takeoutBytes(contentsSize(have)), len(missing), takeoutBytes(contentsSize(missing)))
	if len(missing) > 0 {
		fmt.Fprintf(out, "  missing, by extension: %s\n", byExtension(missing))
	}
	if mode == "stats" {
		if c.verbose {
			for _, mc := range missing {
				fmt.Fprintf(out, "  missing %s (%s)\n", mc.Entries[0].File.Name, takeoutBytes(mc.Size))
			}
		}
		return nil
	}

	if len(missing) == 0 {
		return nil
	}
	log.Printf("Uploading %d files (%s)...", len(missing), takeoutBytes(contentsSize(missing)))
	pr = newTakeoutProgress(len(missing))
	done := 0
	failed, err := takeout.Upload(ctx, st, missing, c.workers, func(mc *takeout.Content, err error) {
		done++
		name := mc.Entries[0].File.Name
		if err != nil {
			log.Printf("Error uploading %s: %v", name, err)
		} else if c.verbose {
			log.Printf("Uploaded %s (%s)", name, takeoutBytes(mc.Size))
		}
		pr.update(done)
	})
	if err != nil {
		return err
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d files failed to upload", failed, len(missing))
	}
	log.Printf("Uploaded %d files.", len(missing))
	return nil
}

// exports returns the exports from the local directory or Google Drive.
func (c *takeoutCmd) exports(ctx context.Context) ([]*takeout.Export, error) {
	if c.dir != "" {
		return takeout.LocalExports(c.dir)
	}
	hc, err := takeoutDriveClient(ctx)
	if err != nil {
		return nil, err
	}
	d := &takeout.Drive{Client: hc, CacheDir: filepath.Join(osutil.CacheDir(), "takeout-toc")}
	return d.Exports(ctx, c.driveFolder)
}

func contentsSize(cs []*takeout.Content) int64 {
	var n int64
	for _, c := range cs {
		n += c.Size
	}
	return n
}

// byExtension summarizes cs by their first entries' file extensions,
// largest total first.
func byExtension(cs []*takeout.Content) string {
	type ext struct {
		name  string
		n     int
		bytes int64
	}
	m := make(map[string]*ext)
	for _, c := range cs {
		name := strings.ToLower(path.Ext(c.Entries[0].File.Name))
		if name == "" {
			name = "(none)"
		}
		e, ok := m[name]
		if !ok {
			e = &ext{name: name}
			m[name] = e
		}
		e.n++
		e.bytes += c.Size
	}
	var exts []*ext
	for _, e := range m {
		exts = append(exts, e)
	}
	slices.SortFunc(exts, func(a, b *ext) int { return cmp.Or(cmp.Compare(b.bytes, a.bytes), cmp.Compare(a.name, b.name)) })
	var parts []string
	for _, e := range exts {
		parts = append(parts, fmt.Sprintf("%s %d (%s)", e.name, e.n, takeoutBytes(e.bytes)))
	}
	return strings.Join(parts, ", ")
}

func takeoutBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n/div >= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// takeoutProgress logs progress through a number of items, at most every
// few seconds.
type takeoutProgress struct {
	total int
	start time.Time
	last  time.Time
}

func newTakeoutProgress(total int) *takeoutProgress {
	now := time.Now()
	return &takeoutProgress{total: total, start: now, last: now}
}

func (p *takeoutProgress) update(done int) {
	now := time.Now()
	if now.Sub(p.last) < 5*time.Second || done == p.total {
		return
	}
	p.last = now
	elapsed := now.Sub(p.start)
	eta := time.Duration(float64(elapsed) / float64(done) * float64(p.total-done))
	log.Printf("%d of %d done; about %v left", done, p.total, eta.Round(time.Second))
}

// takeoutStore is a takeout.Store of a Perkeep server.
type takeoutStore struct {
	c          *client.Client
	permanodes bool
	signer     *schema.Signer // to sign permanodes, if permanodes
	lim        *upload.Limiter
}

// checkServer checks that the server indexes files' digests. A server
// that doesn't would ignore the Digest of a query, and match any file of
// the same size.
func (s *takeoutStore) checkServer(ctx context.Context) error {
	_, err := s.c.Query(ctx, &search.SearchQuery{
		Constraint: &search.Constraint{File: &search.FileConstraint{Digest: "crc32-invalid"}},
		Limit:      1,
	})
	if err == nil || !strings.Contains(err.Error(), "invalid digest") {
		return fmt.Errorf("the server doesn't support searching files by digest; it needs upgrading (to index schema version 11 or later). Checking returned error: %v", err)
	}
	return nil
}

func (s *takeoutStore) Has(ctx context.Context, size int64, crc32 uint32) (bool, error) {
	res, err := s.c.Query(ctx, &search.SearchQuery{
		Constraint: &search.Constraint{File: &search.FileConstraint{
			FileSize: &search.IntConstraint{Equals: &size},
			Digest:   fmt.Sprintf("crc32-%08x", crc32),
		}},
		Limit: 1,
	})
	if err != nil {
		return false, err
	}
	return len(res.Blobs) > 0, nil
}

func (s *takeoutStore) Put(ctx context.Context, e takeout.Entry, r io.Reader) error {
	modTime := e.File.Modified
	res, err := upload.NewFileFromReader(s.c, path.Base(e.File.Name), r, &upload.Options{
		Limiter: s.lim,
		ModTime: modTime,
	}).Run(ctx)
	if err != nil {
		return err
	}
	if !s.permanodes {
		return nil
	}
	if len(res.WholeRefs) == 0 {
		return fmt.Errorf("no whole-file ref for %v to make its permanode", res.FileRef)
	}
	if modTime.IsZero() {
		modTime = time.Now()
	}
	return s.putPermanode(ctx, res.WholeRefs[0].String(), res.FileRef.String(), modTime)
}

// putPermanode uploads the planned permanode of contents with the
// whole-file ref sum, and its camliContent claim of fileRef, as pk-put
// -filenodes does, so the two make the same permanode for the same
// contents.
func (s *takeoutStore) putPermanode(ctx context.Context, sum, fileRef string, claimTime time.Time) error {
	pn, err := schema.NewPlannedPermanode(sum).SignAt(ctx, s.signer, time.Unix(0, 0))
	if err != nil {
		return fmt.Errorf("signing planned permanode: %w", err)
	}
	pnRes, err := s.c.Upload(ctx, client.NewUploadHandleFromString(pn))
	if err != nil {
		return fmt.Errorf("uploading planned permanode: %w", err)
	}
	claim := schema.NewSetAttributeClaim(pnRes.BlobRef, "camliContent", fileRef)
	claim.SetClaimDate(claimTime)
	signed, err := claim.SignAt(ctx, s.signer, claimTime)
	if err != nil {
		return fmt.Errorf("signing camliContent claim: %w", err)
	}
	if _, err := s.c.Upload(ctx, client.NewUploadHandleFromString(signed)); err != nil {
		return fmt.Errorf("uploading camliContent claim: %w", err)
	}
	return nil
}

func takeoutConfigDir() string {
	dir, err := osutil.PerkeepConfigDir()
	if err != nil {
		log.Fatal(err)
	}
	return dir
}

// takeoutDriveClient returns an HTTP client authorized to read Google
// Drive, with the OAuth client ID in the config directory, and a token
// saved there, getting one in a browser the first time.
func takeoutDriveClient(ctx context.Context) (*http.Client, error) {
	dir := takeoutConfigDir()
	clientFile := filepath.Join(dir, "takeout-oauth-client.json")
	tokenFile := filepath.Join(dir, "takeout-oauth-token.json")
	clientJSON, err := os.ReadFile(clientFile)
	if err != nil {
		return nil, fmt.Errorf("reading Google OAuth client ID: %w; see pk takeout -help", err)
	}
	conf, err := google.ConfigFromJSON(clientJSON, takeout.DriveScope)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", clientFile, err)
	}
	tok := new(oauth2.Token)
	if data, err := os.ReadFile(tokenFile); err == nil {
		err = json.Unmarshal(data, tok)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", tokenFile, err)
		}
	} else {
		tok, err = takeoutAuthorize(ctx, conf)
		if err != nil {
			return nil, err
		}
		data, err := json.Marshal(tok)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(tokenFile, data, 0o600); err != nil {
			return nil, err
		}
	}
	return conf.Client(ctx, tok), nil
}

// takeoutAuthorize gets an OAuth token for conf from the user in a
// browser, which is redirected back to a server on localhost.
func takeoutAuthorize(ctx context.Context, conf *oauth2.Config) (*oauth2.Token, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	defer ln.Close()
	conf.RedirectURL = "http://" + ln.Addr().String() + "/"
	var stateBytes [16]byte
	rand.Read(stateBytes[:])
	state := hex.EncodeToString(stateBytes[:])
	verifier := oauth2.GenerateVerifier()

	type result struct {
		code string
		err  error
	}
	resc := make(chan result, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		var res result
		switch {
		case r.FormValue("state") != state:
			res.err = errors.New("OAuth redirect with the wrong state")
		case r.FormValue("error") != "":
			res.err = fmt.Errorf("authorization failed: %s", r.FormValue("error"))
		default:
			res.code = r.FormValue("code")
		}
		if res.err != nil {
			http.Error(w, res.err.Error(), http.StatusBadRequest)
		} else {
			fmt.Fprintln(w, "Authorized. You can close this window.")
		}
		select {
		case resc <- res:
		default:
		}
	})}
	go srv.Serve(ln)
	defer srv.Close()

	fmt.Fprintf(cmdmain.Stderr, "To let pk read Google Drive, open this URL in a browser on this machine:\n\n%s\n\n",
		conf.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.S256ChallengeOption(verifier)))
	var res result
	select {
	case res = <-resc:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if res.err != nil {
		return nil, res.err
	}
	return conf.Exchange(ctx, res.code, oauth2.VerifierOption(verifier))
}
