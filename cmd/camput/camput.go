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
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/remote"
	"camlistore.org/pkg/client"
	"camlistore.org/pkg/jsonsign"
	"camlistore.org/pkg/schema"
)

const buffered = 16 // arbitrary

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
}

func (up *Uploader) lstat(path string) (os.FileInfo, error) {
	// TODO(bradfitz): use VFS
	return os.Lstat(path)
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

func (up *Uploader) uploadNode(n *node) (*client.PutResult, error) {
	fi := n.fi
	m := schema.NewCommonFileMap(n.fullPath, fi)
	mode := fi.Mode()
	switch {
	case mode&os.ModeSymlink != 0:
		// TODO(bradfitz): use VFS here; PopulateSymlinkMap uses os.Readlink directly.
		if err := schema.PopulateSymlinkMap(m, n.fullPath); err != nil {
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
	case mode&os.ModeType == 0: // regular file
		m["camliType"] = "file"

		file, err := up.open(n.fullPath)
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
		for _, c := range n.children {
			pr, err := c.PutResult()
			if err != nil {
				return nil, fmt.Errorf("Error populating directory static set for child %q: %v", c.fullPath, err)
			}
			ss.Add(pr.BlobRef)
		}
		sspr, err := up.UploadMap(ss.Map())
		if err != nil {
			return nil, err
		}
		schema.PopulateDirectoryMap(m, sspr.BlobRef)
	}

	mappr, err := up.UploadMap(m)
	if err == nil {
		if !mappr.Skipped {
			vlog.Printf("Uploaded %q, %s for %s", m["camliType"], mappr.BlobRef, n.fullPath)
		}
	} else {
		vlog.Printf("Error uploading map %v: %v", m, err)
	}
	return mappr, err

}

func (up *Uploader) UploadFile(filename string) (respr *client.PutResult, outerr error) {
	fi, err := up.lstat(filename)
	if err != nil {
		return nil, err
	}

	if fi.IsDir() {
		panic("must use UploadTree now for directories")
	}
	n := &node{
		fullPath: filename, // TODO(bradfitz): resolve this to an abspath
		fi:       fi,
	}
	return up.uploadNode(n)
}

// StartTreeUpload begins uploading dir and all its children.
func (up *Uploader) NewTreeUpload(dir string) *TreeUpload {
	return &TreeUpload{
		base:     dir,
		up:       up,
		donec:    make(chan bool, 1),
		errc:     make(chan error, 1),
		stattedc: make(chan *node, buffered),
	}
}

func (t *TreeUpload) Start() {
	go t.run()
}

type node struct {
	tu       *TreeUpload // nil if not doing a tree upload
	fullPath string
	fi       os.FileInfo
	children []*node

	// cond (and its &mu Lock) guard err and res.
	cond sync.Cond // with L being &mu
	mu   sync.Mutex
	err  error
	res  *client.PutResult

	sumBytes int64 // cached value, if non-zero. also guarded by mu.
}

func (n *node) String() string {
	if n == nil {
		return "<nil *node>"
	}
	return fmt.Sprintf("[node %s, isDir=%v, nchild=%d]", n.fullPath, n.fi.IsDir(), len(n.children))
}

func (n *node) SetPutResult(res *client.PutResult, err error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.res, n.err = res, err
	n.cond.Signal()
}

func (n *node) PutResult() (*client.PutResult, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for n.err == nil && n.res == nil {
		n.cond.Wait()
	}
	return n.res, n.err
}

func (n *node) SumBytes() (v int64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.sumBytes != 0 {
		return n.sumBytes
	}
	for _, c := range n.children {
		v += c.SumBytes()
	}
	if n.fi.Mode()&os.ModeType == 0 {
		v += n.fi.Size()
	}
	n.sumBytes = v
	return
}

type stats struct {
	files, bytes int64
}

func (s *stats) incr(n *node) {
	s.files++
	if !n.fi.IsDir() {
		s.bytes += n.fi.Size()
	}
}

/*
A TreeUpload holds the state of an ongoing recursive directory tree
upload.  Call Wait to get the final result.

Uploading a directory tree involves several concurrent processes, each
which may involve multiple goroutines:

1) one process stats all files and walks all directories as fast as possible
   to calculate how much total work there will be.  this goroutine also
   filters out directories to be skipped. (caches, temp files, skipDirs, etc)

 2) one process works though the files that were discovered and checks
    the statcache to see what actually needs to be uploaded.
    The statcache is
        full path => {last os.FileInfo signature, put result from last time}
    and is used to avoid re-reading/digesting the file even locally,
    trusting that it's already on the server.

 3) one process uploads files & metadata.  This process checks the "havecache"
    to see which blobs are already on the server.  For awhile the local havecache
    (if configured) and the remote blobserver "stat" RPC are raced to determine
    if the local havecache is even faster. If not, it's not consulted. But if the
    latency of remote stats is high enough, checking locally is preferred.
*/
type TreeUpload struct {
	// If DiskUsageMode is set true before Start, only
	// per-directory disk usage stats are output, like the "du"
	// command.
	DiskUsageMode bool

	// Immutable:
	base     string // base directory
	up       *Uploader
	stattedc chan *node // from stat-the-world goroutine to run()

	donec chan bool // closed when run() finishes
	err   error
	errc  chan error // with 1 buffer item

	// Owned by run goroutine:
	total    stats // total bytes on disk
	skipped  stats // not even tried to upload (trusting stat cache)
	uploaded stats // uploaded (even if server said it already had it and bytes weren't sent)

	finalPutRes *client.PutResult // set after run() returns
}

// fi is optional (will be statted if nil)
func (t *TreeUpload) statPath(fullPath string, fi os.FileInfo) (nod *node, err error) {
	defer func() {
		if err == nil && nod != nil {
			t.stattedc <- nod
		}
	}()
	if fi == nil {
		fi, err = t.up.lstat(fullPath)
		if err != nil {
			return nil, err
		}
	}
	n := &node{
		tu:       t,
		fullPath: fullPath,
		fi:       fi,
	}
	n.cond.L = &n.mu

	if !fi.IsDir() {
		return n, nil
	}
	f, err := t.up.open(fullPath)
	if err != nil {
		return nil, err
	}
	fis, err := f.Readdir(-1)
	f.Close()
	if err != nil {
		return nil, err
	}
	sort.Sort(byFileName(fis))
	for _, fi := range fis {
		depn, err := t.statPath(filepath.Join(fullPath, filepath.Base(fi.Name())), fi)
		if err != nil {
			return nil, err
		}
		n.children = append(n.children, depn)
	}
	return n, nil
}

const uploadWorkers = 5

func (t *TreeUpload) run() {
	defer close(t.donec)

	// Kick off scanning all files, eventually learning the root
	// node (which references all its children).
	var root *node // nil until received and set in loop below.
	rootc := make(chan *node, 1)
	go func() {
		n, err := t.statPath(t.base, nil)
		if err != nil {
			log.Fatalf("Error scanning files under %s: %v", t.base, err)
		}
		close(t.stattedc)
		rootc <- n
	}()

	var lastStat, lastUpload string
	dumpStats := func() {
		statStatus := ""
		if root == nil {
			statStatus = fmt.Sprintf("last stat: %s", lastStat)
		}
		log.Printf("Total: %+v Skipped: %+v Uploaded: %+v %s last upload: %s", t.total, t.skipped, t.uploaded, statStatus, lastUpload)
	}

	// Channels for stats & progress bars. These are never closed:
	uploadedc := make(chan *node) // at least tried to upload; server might have had blob
	skippedc := make(chan *node)  // didn't even hit blobserver; trusted our stat cache

	uploadsdonec := make(chan bool)
	var upload chan<- *node
	if t.DiskUsageMode {
		upload = NewNodeWorker(1, func(n *node, ok bool) {
			if !ok {
				uploadsdonec <- true
				return
			}
			if n.fi.IsDir() {
				fmt.Printf("%d\t%s\n", n.SumBytes()>>10, n.fullPath)
			}
		})
	} else {
		upload = NewNodeWorker(uploadWorkers, func(n *node, ok bool) {
			if !ok {
				log.Printf("done with all uploads.")
				uploadsdonec <- true
				return
			}
			put, err := t.up.uploadNode(n)
			if err != nil {
				log.Fatalf("Error uploading %s: %v", n.fullPath, err)
			}
			n.SetPutResult(put, nil)
			if c := t.up.statCache; c != nil && !n.fi.IsDir() {
				c.AddCachedPutResult(t.up.pwd, n.fullPath, n.fi, put)
			}
			uploadedc <- n
		})
	}

	checkStatCache := NewNodeWorker(10, func(n *node, ok bool) {
		if !ok {
			if t.up.statCache != nil {
				log.Printf("done checking stat cache")
			}
			close(upload)
			return
		}
		if t.DiskUsageMode || t.up.statCache == nil {
			log.Printf("skip cache check %v, %v", t.DiskUsageMode, t.up.statCache)
			upload <- n
			return
		}
		if !n.fi.IsDir() {
			cachedRes, err := t.up.statCache.CachedPutResult(t.up.pwd, n.fullPath, n.fi)
			if err == nil {
				n.SetPutResult(cachedRes, nil)
				cachelog.Printf("Cache HIT on %q -> %v", n.fullPath, cachedRes)
				skippedc <- n
				return
			}
		}
		upload <- n
	})

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	stattedc := t.stattedc
Loop:
	for {
		select {
		case <-uploadsdonec:
			break Loop
		case n := <-rootc:
			root = n
		case n := <-uploadedc:
			t.uploaded.incr(n)
			lastUpload = n.fullPath
		case n := <-skippedc:
			t.skipped.incr(n)
		case n, ok := <-stattedc:
			if !ok {
				log.Printf("done stattting:")
				dumpStats()
				close(checkStatCache)
				stattedc = nil
				continue
			}
			lastStat = n.fullPath
			t.total.incr(n)
			checkStatCache <- n
		case <-ticker.C:
			dumpStats()
		}
	}

	log.Printf("tree upload finished. final stats:")
	dumpStats()

	if root == nil {
		panic("unexpected nil root node")
	}
	var err error
	log.Printf("Waiting on root node %q", root.fullPath)
	t.finalPutRes, err = root.PutResult()
	log.Printf("Waited on root node %q: %v", root.fullPath, t.finalPutRes)
	if err != nil {
		t.err = err
	}
}

func (t *TreeUpload) Wait() (*client.PutResult, error) {
	<-t.donec
	// If an error is waiting and we don't otherwise have one, use it:
	if t.err == nil {
		select {
		case t.err = <-t.errc:
		default:
		}
	}
	if t.err == nil && t.finalPutRes == nil {
		panic("Nothing ever set t.finalPutRes, but no error set")
	}
	return t.finalPutRes, t.err
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

type byFileName []os.FileInfo

func (s byFileName) Len() int           { return len(s) }
func (s byFileName) Less(i, j int) bool { return s[i].Name() < s[j].Name() }
func (s byFileName) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

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
