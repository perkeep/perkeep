/*
Copyright 2011 Google Inc.

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
	"bytes"
	"crypto/sha1"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/remote"
	"camlistore.org/pkg/client"
	"camlistore.org/pkg/jsonsign"
	"camlistore.org/pkg/schema"
)

const buffered = 16 // arbitrary

// Things that can be uploaded.  (at most one of these)
//var flagRemove = flag.Bool("remove", false, "remove the list of blobrefs")

var (
	flagVerbose = flag.Bool("verbose", false, "extra debug logging")
)

var ErrUsage = UsageError("invalid command usage")

type UsageError string

func (ue UsageError) Error() string {
	return "Usage error: " + string(ue)
}

type CommandRunner interface {
	Usage()
	RunCommand(up *Uploader, args []string) error
}

type Exampler interface {
	Examples() []string
}

var modeCommand = make(map[string]CommandRunner)
var modeFlags = make(map[string]*flag.FlagSet)

func RegisterCommand(mode string, makeCmd func(Flags *flag.FlagSet) CommandRunner) {
	if _, dup := modeCommand[mode]; dup {
		log.Fatalf("duplicate command %q registered", mode)
	}
	flags := flag.NewFlagSet(mode+" options", flag.ContinueOnError)
	flags.Usage = func() {}
	modeFlags[mode] = flags
	modeCommand[mode] = makeCmd(flags)
}

// wereErrors gets set to true if any error was encountered, which
// changes the os.Exit value
var wereErrors = false

// UploadCache is the "stat cache" for regular files.  Given a current
// working directory, possibly relative filename, and stat info,
// returns what the ultimate put result (the top-level "file" schema
// blob) for that regular file was.
type UploadCache interface {
	CachedPutResult(pwd, filename string, fi os.FileInfo) (*client.PutResult, error)
	AddCachedPutResult(pwd, filename string, fi os.FileInfo, pr *client.PutResult)
}

type HaveCache interface {
	BlobExists(br *blobref.BlobRef) bool
	NoteBlobExists(br *blobref.BlobRef)
}

type Uploader struct {
	*client.Client

	rollSplits bool // rolling checksum file splitting

	// for debugging; normally nil, but overrides Client if set
	// TODO(bradfitz): clean this up? embed a StatReceiver instead
	// of a Client?
	altStatReceiver blobserver.StatReceiver

	entityFetcher jsonsign.EntityFetcher

	transport *tinkerTransport // for HTTP statistics
	pwd       string
	statCache UploadCache
	haveCache HaveCache

	fs http.FileSystem // virtual filesystem to read from; nil means OS filesystem.

	filecapc chan bool
}

func (up *Uploader) stat(path string) (os.FileInfo, error) {
	if up.fs == nil {
		return os.Stat(path)
	}
	f, err := up.fs.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Stat()
}

func (up *Uploader) open(path string) (http.File, error) {
	if up.fs == nil {
		return os.Open(path)
	}
	return up.fs.Open(path)
}

func blobDetails(contents io.ReadSeeker) (bref *blobref.BlobRef, size int64, err error) {
	s1 := sha1.New()
	contents.Seek(0, 0)
	size, err = io.Copy(s1, contents)
	if err == nil {
		bref = blobref.FromHash("sha1", s1)
	}
	return
}

func (up *Uploader) UploadFileBlob(filename string) (*client.PutResult, error) {
	var (
		err  error
		size int64
		ref  *blobref.BlobRef
		body io.Reader
	)
	if filename == "-" {
		buf := bytes.NewBuffer(make([]byte, 0))
		size, err = io.Copy(buf, os.Stdin)
		if err != nil {
			return nil, err
		}
		// TODO(bradfitz,mpl): limit this buffer size?
		file := buf.Bytes()
		s1 := sha1.New()
		size, err = io.Copy(s1, buf)
		if err != nil {
			return nil, err
		}
		ref = blobref.FromHash("sha1", s1)
		body = io.LimitReader(bytes.NewBuffer(file), size)
	} else {
		fi, err := up.stat(filename)
		if err != nil {
			return nil, err
		}
		if fi.Mode()&os.ModeType != 0 {
			return nil, fmt.Errorf("%q is not a regular file", filename)
		}
		file, err := up.open(filename)
		if err != nil {
			return nil, err
		}
		ref, size, err = blobDetails(file)
		if err != nil {
			return nil, err
		}
		file.Seek(0, 0)
		body = io.LimitReader(file, size)
	}

	handle := &client.UploadHandle{ref, size, body}
	return up.Upload(handle)
}

func (up *Uploader) getUploadToken() {
	up.filecapc <- true
}

func (up *Uploader) releaseUploadToken() {
	<-up.filecapc
}

func (up *Uploader) UploadFile(filename string) (respr *client.PutResult, outerr error) {
	up.getUploadToken()
	defer up.releaseUploadToken()

	// TODO(bradfitz): use VFS here.
	fi, err := os.Lstat(filename)
	if err != nil {
		return nil, err
	}

	if up.statCache != nil && !fi.IsDir() {
		cachedRes, err := up.statCache.CachedPutResult(up.pwd, filename, fi)
		if err == nil {
			cachelog.Printf("Cache HIT on %q -> %v", filename, cachedRes)
			return cachedRes, nil
		}
		defer func() {
			if respr != nil && outerr == nil {
				up.statCache.AddCachedPutResult(up.pwd, filename, fi, respr)
			}
		}()
	}

	m := schema.NewCommonFileMap(filename, fi)
	mode := fi.Mode()
	switch {
	case mode&os.ModeSymlink != 0:
		// TODO(bradfitz): use VFS here; PopulateSymlinkMap uses os.Readlink directly.
		if err = schema.PopulateSymlinkMap(m, filename); err != nil {
			return nil, err
		}
	case mode&os.ModeDevice != 0:
		// including mode & os.ModeCharDevice
		fallthrough
	case mode&os.ModeSocket != 0:
		fallthrough
	case mode&os.ModeNamedPipe != 0: // FIFO
		fallthrough
	default:
		return nil, schema.ErrUnimplemented
	case !fi.IsDir():
		m["camliType"] = "file"

		file, err := up.open(filename)
		if err != nil {
			return nil, err
		}
		defer file.Close()

		statReceiver := up.altStatReceiver
		if statReceiver == nil {
			// TODO(bradfitz): just make Client be a
			// StatReceiver? move remote's ReceiveBlob ->
			// Upload wrapper into Client itself?
			statReceiver = remote.NewFromClient(up.Client)
		}

		schemaWriteFileMap := schema.WriteFileMap
		if up.rollSplits {
			schemaWriteFileMap = schema.WriteFileMapRolling
		}
		blobref, err := schemaWriteFileMap(statReceiver, m, io.LimitReader(file, fi.Size()))
		if err != nil {
			return nil, err
		}
		// TODO(bradfitz): taking a PutResult here is kinda
		// gross.  should instead make a blobserver.Storage
		// wrapper type that can track some of this?  or that
		// updates the client stats directly or something.
		{
			json, _ := schema.MapToCamliJSON(m)
			pr := &client.PutResult{BlobRef: blobref, Size: int64(len(json)), Skipped: false}
			return pr, nil
		}
	case fi.IsDir():
		ss := new(schema.StaticSet)
		dir, err := up.open(filename)
		if err != nil {
			return nil, err
		}
		var dirNames []string
		fis, err := dir.Readdir(-1)
		if err != nil {
			return nil, err
		}
		dir.Close()
		for _, fi := range fis {
			dirNames = append(dirNames, fi.Name())
		}
		sort.Strings(dirNames)

		// Temporarily give up our upload token while we
		// process all our children.  The defer function makes
		// sure we re-acquire it (keeping balance in the
		// world) before we return.
		up.releaseUploadToken()
		tokenTookBack := false
		defer func() {
			if !tokenTookBack {
				up.getUploadToken()
			}
		}()

		rate := make(chan bool, 100) // max outstanding goroutines, further limited by filecapc
		type nameResult struct {
			name   string
			putres *client.PutResult
			err    error
		}

		resc := make(chan nameResult, buffered)
		go func() {
			for _, name := range dirNames {
				rate <- true
				go func(dirEntName string) {
					pr, err := up.UploadFile(filename + "/" + dirEntName)
					if pr == nil && err == nil {
						log.Fatalf("nil/nil from up.UploadFile on %q", filename+"/"+dirEntName)
					}
					resc <- nameResult{dirEntName, pr, err}
					<-rate
				}(name)
			}
		}()
		resm := make(map[string]*client.PutResult)
		var entUploadErr error
		for _ = range dirNames {
			r := <-resc
			if r.err != nil {
				entUploadErr = fmt.Errorf("error uploading %s: %v", r.name, r.err)
				continue
			}
			resm[r.name] = r.putres
		}
		if entUploadErr != nil {
			return nil, entUploadErr
		}
		for _, name := range dirNames {
			ss.Add(resm[name].BlobRef)
		}

		// Re-acquire the upload token that we temporarily yielded up above.
		up.getUploadToken()
		tokenTookBack = true

		sspr, err := up.UploadMap(ss.Map())
		if err != nil {
			return nil, err
		}
		schema.PopulateDirectoryMap(m, sspr.BlobRef)
	}

	mappr, err := up.UploadMap(m)
	if err == nil {
		vlog.Printf("Uploaded %q, %s for %s", m["camliType"], mappr.BlobRef, filename)
	} else {
		vlog.Printf("Error uploading map %v: %v", m, err)
	}
	return mappr, err
}

func (up *Uploader) StartTreeUpload(path string) *TreeUpload {
	t := &TreeUpload{
		up:    up,
		donec: make(chan bool),
	}
	go t.start()
	return t
}

type TreeUpload struct {
	up    *Uploader
	donec chan bool
}

func (t *TreeUpload) start() {
	defer close(t.donec)
	/*
Plan:
	in one goroutine, walk/stat all files as fast as possible
	(parallel stats/readdirs) and calculate stats / full tree in
	memory. (future: optional for huge trees?). this one should
	finish first, but might still take minutes and the other
	network-bound goroutines should start immediately.

	other goroutine calculates which files/dirs need to be
	uploaded based on the stat cache (not yet digesting the files
	and doing splits, that would cause that work to be done twice,
	and it might change in the meantime) for regular files, the
	stat cache is:

	  path => {last fileinfo size/modtime, put result from last time}

	final goroutine(s) do the actual uploads.

	The idea is that the user can see (console / web) three
        progress bars: 1) statting the world and getting one upper
        bound on time/bytes/counts, then 2) revised (likely lower)
        upper bound on actual counts to upload, and then 3) uploads in
        progress.
	 */
}

func (t *TreeUpload) Wait() (*client.PutResult, error) {
	<-t.donec
	return nil, fmt.Errorf("TODO: implement")
}

func (up *Uploader) SignMap(m map[string]interface{}) (string, error) {
	camliSigBlobref := up.Client.SignerPublicKeyBlobref()
	if camliSigBlobref == nil {
		// TODO: more helpful error message
		return "", errors.New("No public key configured.")
	}

	m["camliSigner"] = camliSigBlobref.String()
	unsigned, err := schema.MapToCamliJSON(m)
	if err != nil {
		return "", err
	}
	sr := &jsonsign.SignRequest{
		UnsignedJson:  unsigned,
		Fetcher:       up.Client.GetBlobFetcher(),
		EntityFetcher: up.entityFetcher,
	}
	return sr.Sign()
}

func (up *Uploader) UploadMap(m map[string]interface{}) (*client.PutResult, error) {
	json, err := schema.MapToCamliJSON(m)
	if err != nil {
		return nil, err
	}
	return up.uploadString(json)
}

func (up *Uploader) UploadAndSignMap(m map[string]interface{}) (*client.PutResult, error) {
	signed, err := up.SignMap(m)
	if err != nil {
		return nil, err
	}
	return up.uploadString(signed)
}

func (up *Uploader) uploadString(s string) (*client.PutResult, error) {
	uh := client.NewUploadHandleFromString(s)
	if c := up.haveCache; c != nil && c.BlobExists(uh.BlobRef) {
		cachelog.Printf("HaveCache HIT for %s / %d", uh.BlobRef, uh.Size)
		return &client.PutResult{BlobRef: uh.BlobRef, Size: uh.Size, Skipped: true}, nil
	}
	pr, err := up.Upload(uh)
	if err == nil && up.haveCache != nil {
		up.haveCache.NoteBlobExists(uh.BlobRef)
	}
	if pr == nil && err == nil {
		log.Fatalf("Got nil/nil in uploadString while uploading %s", s)
	}
	return pr, err
}

func (up *Uploader) UploadNewPermanode() (*client.PutResult, error) {
	unsigned := schema.NewUnsignedPermanode()
	return up.UploadAndSignMap(unsigned)
}

type namedMode struct {
	Name    string
	Command CommandRunner
}

func allModes(startModes []string) <-chan namedMode {
	ch := make(chan namedMode)
	go func() {
		defer close(ch)
		done := map[string]bool{}
		for _, name := range startModes {
			done[name] = true
			cmd := modeCommand[name]
			if cmd == nil {
				panic("bogus mode: " + name)
			}
			ch <- namedMode{name, cmd}
		}
		for name, cmd := range modeCommand {
			if !done[name] {
				ch <- namedMode{name, cmd}
			}
		}
	}()
	return ch
}

func errf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format, args...)
}

func usage(msg string) {
	if msg != "" {
		errf("Error: %v\n", msg)
	}
	errf(`
Usage: camput [globalopts] <mode> [commandopts] [commandargs]

Examples:
`)
	order := []string{"init", "file", "permanode", "blob", "attr"}
	for mode := range allModes(order) {
		errf("\n")
		if ex, ok := mode.Command.(Exampler); ok {
			for _, example := range ex.Examples() {
				errf("  camput %s %s\n", mode.Name, example)
			}
		} else {
			errf("  camput %s ...\n", mode.Name)
		}
	}

	// TODO(bradfitz): move these to Examples
	/*
	  camput share [opts] <blobref to share via haveref>

	  camput blob <files>     (raw, without any metadata)
	  camput blob -           (read from stdin)

	  camput attr <permanode> <name> <value>         Set attribute
	  camput attr --add <permanode> <name> <value>   Adds attribute (e.g. "tag")
	  camput attr --del <permanode> <name> [<value>] Deletes named attribute [value]
	*/

	errf(`
For mode-specific help:

  camput <mode> -help

Global options:
`)
	flag.PrintDefaults()
	os.Exit(1)
}

func handleResult(what string, pr *client.PutResult, err error) error {
	if err != nil {
		log.Printf("Error putting %s: %s", what, err)
		wereErrors = true
		return err
	}
	fmt.Println(pr.BlobRef.String())
	return nil
}

func makeUploader() *Uploader {
	cc := client.NewOrFail()
	if !*flagVerbose {
		cc.SetLogger(nil)
	}

	transport := new(tinkerTransport)
	transport.transport = &http.Transport{DisableKeepAlives: false}
	cc.SetHttpClient(&http.Client{Transport: transport})

	pwd, err := os.Getwd()
	if err != nil {
		log.Fatalf("os.Getwd: %v", err)
	}

	up := &Uploader{
		Client:    cc,
		transport: transport,
		pwd:       pwd,
		filecapc:  make(chan bool, 10 /* TODO: config option on max files at a time */),
		entityFetcher: &jsonsign.CachingEntityFetcher{
			Fetcher: &jsonsign.FileEntityFetcher{File: cc.SecretRingFile()},
		},
	}
	return up
}

func hasFlags(flags *flag.FlagSet) bool {
	any := false
	flags.VisitAll(func(*flag.Flag) {
		any = true
	})
	return any
}

var saveHooks []func()

func AddSaveHook(fn func()) {
	saveHooks = append(saveHooks, fn)
}

func Save() {
	for _, fn := range saveHooks {
		fn()
	}
	saveHooks = nil
}

func main() {
	defer Save()
	jsonsign.AddFlags()
	client.AddFlags()
	flag.Parse()

	if flag.NArg() == 0 {
		usage("No mode given.")
	}

	mode := flag.Arg(0)
	cmd, ok := modeCommand[mode]
	if !ok {
		usage(fmt.Sprintf("Unknown mode %q", mode))
	}

	var up *Uploader
	if mode != "init" {
		up = makeUploader()
	}

	cmdFlags := modeFlags[mode]
	err := cmdFlags.Parse(flag.Args()[1:])
	if err != nil {
		err = ErrUsage
	} else {
		err = cmd.RunCommand(up, cmdFlags.Args())
	}
	if ue, isUsage := err.(UsageError); isUsage {
		if isUsage {
			errf("%s\n", ue)
		}
		cmd.Usage()
		errf("\nGlobal options:\n")
		flag.PrintDefaults()

		if hasFlags(cmdFlags) {
			errf("\nMode-specific options for mode %q:\n", mode)
			cmdFlags.PrintDefaults()
		}
		os.Exit(1)
	}
	if *flagVerbose {
		stats := up.Stats()
		log.Printf("Client stats: %s", stats.String())
		log.Printf("  #HTTP reqs: %d", up.transport.reqs)
	}
	if err != nil || wereErrors /* TODO: remove this part */ {
		log.Printf("Error: %v", err)
		Save()
		os.Exit(2)
	}
}
