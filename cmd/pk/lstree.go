/*
Copyright 2026 The Perkeep Authors.

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
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"go4.org/types"
	"golang.org/x/sync/errgroup"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/cmdmain"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/search"
)

type lsTreeCmd struct {
	server   string
	at       string
	longRefs bool
	json     bool
}

func init() {
	cmdmain.RegisterMode("ls-tree", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(lsTreeCmd)
		flags.StringVar(&cmd.server, "server", "", "Server to query. "+serverFlagHelp)
		flags.StringVar(&cmd.at, "at", "", "When given a permanode, list its camliContent as of this time. RFC3339 only for now. Empty string means current time.")
		flags.BoolVar(&cmd.longRefs, "full-refs", false, "Print full blobrefs instead of abbreviated ones.")
		flags.BoolVar(&cmd.json, "json", false, "Print one JSON object per entry instead of text.")
		return cmd
	})
}

func (c *lsTreeCmd) Describe() string {
	return "Recursively list a directory, given its blobref or a permanode whose camliContent is one."
}

func (c *lsTreeCmd) Usage() {
	fmt.Fprintf(os.Stderr, "Usage: pk [globalopts] ls-tree [--at=time] [--full-refs] [--json] <dir-or-permanode-blobref>\n%s\n", blobrefArgHelp)
}

func (c *lsTreeCmd) Examples() []string {
	return []string{
		"sha224-638982b0a0928acfc30ff9b0551d7e4e7432596044b79affd4e54f93",
		"-at=2007-05-20T00:00:00Z sha224-440f449ffe6ec2f986926db851433194eeb7298e472c4c166dba6c0d",
	}
}

func (c *lsTreeCmd) RunCommand(args []string) error {
	if len(args) != 1 {
		return cmdmain.UsageError("requires one blobref")
	}
	var at time.Time
	if c.at != "" {
		var err error
		at, err = time.Parse(time.RFC3339, c.at)
		if err != nil {
			return fmt.Errorf("error parsing --at value %q: %w", c.at, err)
		}
	}
	ctx := context.Background()
	cl := newClient(c.server)
	br, err := cl.ResolveRef(ctx, args[0])
	if err != nil {
		return err
	}
	root, err := resolveTreeRoot(ctx, cl, cl, br, at)
	if err != nil {
		return err
	}
	bw := bufio.NewWriter(os.Stdout)
	t := &treeLister{
		fetcher:  cl,
		w:        bw,
		loc:      time.Local,
		longRefs: c.longRefs,
		json:     c.json,
	}
	if err := t.list(ctx, root); err != nil {
		bw.Flush()
		return err
	}
	return bw.Flush()
}

// describer is the subset of *client.Client that resolveTreeRoot uses.
type describer interface {
	Describe(context.Context, *search.DescribeRequest) (*search.DescribeResponse, error)
}

// resolveTreeRoot returns the schema blob to list for br: br itself, or
// if br is a permanode, its camliContent as of at (or now, if zero).
func resolveTreeRoot(ctx context.Context, fetcher blob.Fetcher, d describer, br blob.Ref, at time.Time) (*schema.Blob, error) {
	b, err := fetchSchemaBlob(ctx, fetcher, br)
	if err != nil {
		return nil, err
	}
	if b.Type() != schema.TypePermanode {
		return b, nil
	}
	res, err := d.Describe(ctx, &search.DescribeRequest{
		BlobRef: br,
		At:      types.Time3339(at),
	})
	if err != nil {
		return nil, fmt.Errorf("describing permanode %v: %w", br, err)
	}
	db := res.Meta.Get(br)
	if db == nil || db.Permanode == nil {
		return nil, fmt.Errorf("permanode %v not found in describe response", br)
	}
	content, ok := blob.Parse(db.Permanode.Attr.Get("camliContent"))
	if !ok {
		return nil, fmt.Errorf("permanode %v has no camliContent", br)
	}
	return fetchSchemaBlob(ctx, fetcher, content)
}

func fetchSchemaBlob(ctx context.Context, fetcher blob.Fetcher, br blob.Ref) (*schema.Blob, error) {
	rc, _, err := fetcher.Fetch(ctx, br)
	if err != nil {
		return nil, fmt.Errorf("fetching %v: %w", br, err)
	}
	defer rc.Close()
	b, err := schema.BlobFromReader(br, rc)
	if err != nil {
		return nil, fmt.Errorf("parsing %v: %w", br, err)
	}
	return b, nil
}

// fetchConcurrency is the maximum number of directory entries that a
// treeLister fetches at once.
const fetchConcurrency = 20

// A treeLister writes a listing of a tree of schema blobs, one line per
// entry, depth first with each directory's entries sorted by name.
type treeLister struct {
	fetcher  blob.Fetcher
	w        io.Writer
	loc      *time.Location // for formatting mtimes
	longRefs bool
	json     bool
}

// list writes the listing of root, which may be a directory, file, or
// symlink.
func (t *treeLister) list(ctx context.Context, root *schema.Blob) error {
	return t.walk(ctx, ".", root)
}

func (t *treeLister) walk(ctx context.Context, p string, b *schema.Blob) error {
	if err := t.writeEntry(p, b); err != nil {
		return err
	}
	if b.Type() != schema.TypeDirectory {
		return nil
	}
	children, err := t.readDir(ctx, b)
	if err != nil {
		return fmt.Errorf("%s: %w", p, err)
	}
	for _, c := range children {
		cp := c.FileName()
		if p != "." {
			cp = path.Join(p, cp)
		}
		if err := t.walk(ctx, cp, c); err != nil {
			return err
		}
	}
	return nil
}

// readDir fetches the entries of directory dir, sorted by name.
func (t *treeLister) readDir(ctx context.Context, dir *schema.Blob) ([]*schema.Blob, error) {
	dr, err := schema.NewDirReader(ctx, t.fetcher, dir.BlobRef())
	if err != nil {
		return nil, err
	}
	members, err := dr.StaticSet(ctx)
	if err != nil {
		return nil, err
	}
	children := make([]*schema.Blob, len(members))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(fetchConcurrency)
	for i, m := range members {
		g.Go(func() error {
			b, err := fetchSchemaBlob(gctx, t.fetcher, m)
			children[i] = b
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	sort.Slice(children, func(i, j int) bool {
		return children[i].FileName() < children[j].FileName()
	})
	return children, nil
}

// lsTreeJSONEntry is the format of each line of ls-tree's -json output.
type lsTreeJSONEntry struct {
	Path    string     `json:"path"`
	Type    string     `json:"type"`
	Mode    string     `json:"mode"`
	Size    *int64     `json:"size,omitempty"` // only for files
	ModTime *time.Time `json:"modTime,omitempty"`
	BlobRef blob.Ref   `json:"blobRef"`
	Target  string     `json:"target,omitempty"` // only for symlinks
}

func (t *treeLister) writeEntry(p string, b *schema.Blob) error {
	var target string
	if b.Type() == schema.TypeSymlink {
		sf, ok := b.AsStaticFile()
		if !ok {
			return fmt.Errorf("%s: malformed symlink %v", p, b.BlobRef())
		}
		sl, ok := sf.AsStaticSymlink()
		if !ok {
			return fmt.Errorf("%s: malformed symlink %v", p, b.BlobRef())
		}
		target = sl.SymlinkTargetString()
	}
	mtime := b.ModTime()

	if t.json {
		e := lsTreeJSONEntry{
			Path:    p,
			Type:    string(b.Type()),
			Mode:    fmt.Sprintf("%04o", b.FileMode().Perm()),
			BlobRef: b.BlobRef(),
			Target:  target,
		}
		if b.Type() == schema.TypeFile {
			size := b.PartsSize()
			e.Size = &size
		}
		if !mtime.IsZero() {
			e.ModTime = &mtime
		}
		j, err := json.Marshal(e)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(t.w, "%s\n", j)
		return err
	}

	size := "-"
	if b.Type() == schema.TypeFile {
		size = strconv.FormatInt(b.PartsSize(), 10)
	}
	mtimeStr := "-"
	if !mtime.IsZero() {
		mtimeStr = mtime.In(t.loc).Format("2006-01-02 15:04")
	}
	ref := b.BlobRef().String()
	if !t.longRefs {
		ref = abbrevRef(b.BlobRef())
	}
	name := quoteNameIfNeeded(p)
	if b.Type() == schema.TypeSymlink {
		name += " -> " + quoteNameIfNeeded(target)
	}
	_, err := fmt.Fprintf(t.w, "%s %12s %16s  %s  %s\n", modeString(b.FileMode()), size, mtimeStr, ref, name)
	return err
}

// abbrevRefDigits is the number of hex digits of a digest that
// abbreviated blobrefs keep.
const abbrevRefDigits = 10

// abbrevRef returns br shortened to its hash name and the first
// abbrevRefDigits digits of its digest, like "sha224-0123456789".
func abbrevRef(br blob.Ref) string {
	s := br.String()
	name, digest, ok := strings.Cut(s, "-")
	if !ok || len(digest) <= abbrevRefDigits {
		return s
	}
	return name + "-" + digest[:abbrevRefDigits]
}

// modeString formats m like ls -l does.
func modeString(m os.FileMode) string {
	s := m.String()
	// os.FileMode uses 'L' for symlinks and 'S' for sockets.
	switch s[0] {
	case 'L':
		s = "l" + s[1:]
	case 'S':
		s = "s" + s[1:]
	}
	return s
}

// quoteNameIfNeeded returns s, or s quoted Go-style if it isn't valid
// UTF-8 or contains characters that would garble the listing.
func quoteNameIfNeeded(s string) string {
	if !utf8.ValidString(s) {
		return strconv.Quote(s)
	}
	for _, r := range s {
		if !unicode.IsPrint(r) || r == '"' {
			return strconv.Quote(s)
		}
	}
	return s
}
