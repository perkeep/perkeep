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
	"crypto/sha1"
	"flag"
	"fmt"
	"hash"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/remote"
	"camlistore.org/pkg/client"
	"camlistore.org/pkg/schema"

	"camlistore.org/third_party/github.com/mpl/histo"
)

type fileCmd struct {
	name string
	tag  string

	makePermanode  bool // make new, unique permanode of the root (dir or file)
	filePermanodes bool // make planned permanodes for each file (based on their digest)
	rollSplits     bool
	diskUsage      bool // show "du" disk usage only (dry run mode), don't actually upload

	havecache, statcache bool

	// Go into in-memory stats mode only; doesn't actually upload.
	memstats bool
	histo    string // optional histogram output filename
}

func init() {
	RegisterCommand("file", func(flags *flag.FlagSet) CommandRunner {
		cmd := new(fileCmd)
		flags.BoolVar(&cmd.makePermanode, "permanode", false, "Create an associate a new permanode for the uploaded file or directory.")
		flags.BoolVar(&cmd.filePermanodes, "filenodes", false, "Create (if necessary) content-based permanodes for each uploaded file.")
		flags.StringVar(&cmd.name, "name", "", "Optional name attribute to set on permanode when using -permanode.")
		flags.StringVar(&cmd.tag, "tag", "", "Optional tag(s) to set on permanode when using -permanode. Single value or comma separated.")

		flags.BoolVar(&cmd.statcache, "statcache", false, "Use the stat cache, assuming unchanged files already uploaded in the past are still there. Fast, but potentially dangerous.")
		flags.BoolVar(&cmd.havecache, "havecache", false, "Use the 'have cache', a cache keeping track of what blobs the remote server should already have from previous uploads.")
		flags.BoolVar(&cmd.rollSplits, "rolling", false, "Use rolling checksum file splits.")
		flags.BoolVar(&cmd.memstats, "debug-memstats", false, "Enter debug in-memory mode; collecting stats only. Doesn't upload anything.")
		flags.BoolVar(&cmd.diskUsage, "du", false, "Dry run mode: only show disk usage information, without upload or statting dest. Used for testing skipDirs configs, mostly.")
		flags.StringVar(&cmd.histo, "debug-histogram-file", "", "File where to print the histogram of the blob sizes. Requires debug-memstats.")

		flagCacheLog = flags.Bool("logcache", false, "log caching details")

		return cmd
	})
}

func (c *fileCmd) Usage() {
	fmt.Fprintf(stderr, "Usage: camput [globalopts] file [fileopts] <file/director(ies)>\n")
}

func (c *fileCmd) Examples() []string {
	return []string{
		"[opts] <file(s)/director(ies)",
		"--permanode --name='Homedir backup' --tag=backup,homedir $HOME",
	}
}

func (c *fileCmd) RunCommand(up *Uploader, args []string) error {
	if len(args) == 0 {
		return UsageError("No files or directories given.")
	}
	if c.name != "" && !c.makePermanode {
		return UsageError("Can't set name without using --permanode")
	}
	if c.tag != "" && !c.makePermanode {
		return UsageError("Can't set tag without using --permanode")
	}
	if c.histo != "" && !c.memstats {
		return UsageError("Can't use histo without memstats")
	}
	if c.memstats {
		sr := new(statsStatReceiver)
		if c.histo != "" {
			num := 100
			sr.histo = histo.NewHisto(num)
		}
		up.altStatReceiver = sr
		defer func() { sr.DumpStats(c.histo) }()
	}
	if c.statcache {
		cache := NewFlatStatCache()
		up.statCache = cache
	}
	if c.havecache {
		cache := NewFlatHaveCache()
		up.haveCache = cache
	}

	var (
		permaNode *client.PutResult
		lastPut   *client.PutResult
		err       error
	)
	if c.makePermanode {
		if len(args) != 1 {
			return fmt.Errorf("The --permanode flag can only be used with exactly one file or directory argument")
		}
		permaNode, err = up.UploadNewPermanode()
		if err != nil {
			return fmt.Errorf("Uploading permanode: %v", err)
		}
	}
	if c.diskUsage {
		if len(args) != 1 {
			return fmt.Errorf("The --du flag can only be used with exactly one directory argument")
		}
		dir := args[0]
		fi, err := up.stat(dir)
		if err != nil {
			return err
		}
		if !fi.IsDir() {
			return fmt.Errorf("%q is not a directory.", dir)
		}
		t := up.NewTreeUpload(dir)
		t.DiskUsageMode = true
		t.FilePermanodes = c.filePermanodes
		t.Start()
		pr, err := t.Wait()
		if err != nil {
			return err
		}
		handleResult("tree-upload", pr, err)
		return nil
	}
	if c.rollSplits {
		up.rollSplits = true
	}

	for _, filename := range args {
		fi, err := os.Stat(filename)
		if err != nil {
			return err
		}
		if fi.IsDir() {
			t := up.NewTreeUpload(filename)
			t.FilePermanodes = c.filePermanodes
			t.Start()
			lastPut, err = t.Wait()
		} else {
			lastPut, err = up.UploadFile(filename)
		}
		if handleResult("file", lastPut, err) != nil {
			return err
		}
	}

	if permaNode != nil {
		put, err := up.UploadAndSignMap(schema.NewSetAttributeClaim(permaNode.BlobRef, "camliContent", lastPut.BlobRef.String()))
		if handleResult("claim-permanode-content", put, err) != nil {
			return err
		}
		if c.name != "" {
			put, err := up.UploadAndSignMap(schema.NewSetAttributeClaim(permaNode.BlobRef, "name", c.name))
			handleResult("claim-permanode-name", put, err)
		}
		if c.tag != "" {
			tags := strings.Split(c.tag, ",")
			m := schema.NewSetAttributeClaim(permaNode.BlobRef, "tag", tags[0])
			for _, tag := range tags {
				m = schema.NewAddAttributeClaim(permaNode.BlobRef, "tag", tag)
				put, err := up.UploadAndSignMap(m)
				handleResult("claim-permanode-tag", put, err)
			}
		}
		handleResult("permanode", permaNode, nil)
	}
	return nil
}

// statsStatReceiver is a dummy blobserver.StatReceiver that doesn't store anything;
// it just collects statistics.
type statsStatReceiver struct {
	mu    sync.Mutex
	have  map[string]int64
	histo *histo.Histo
}

func (sr *statsStatReceiver) lock() {
	sr.mu.Lock()
	if sr.have == nil {
		sr.have = make(map[string]int64)
	}
}

func (sr *statsStatReceiver) ReceiveBlob(blob *blobref.BlobRef, source io.Reader) (sb blobref.SizedBlobRef, err error) {
	n, err := io.Copy(ioutil.Discard, source)
	if err != nil {
		return
	}
	sr.lock()
	defer sr.mu.Unlock()
	sr.have[blob.String()] = n
	if sr.histo != nil {
		sr.histo.Add(n)
	}
	return blobref.SizedBlobRef{blob, n}, nil
}

func (sr *statsStatReceiver) StatBlobs(dest chan<- blobref.SizedBlobRef, blobs []*blobref.BlobRef, _ time.Duration) error {
	sr.lock()
	defer sr.mu.Unlock()
	for _, br := range blobs {
		if size, ok := sr.have[br.String()]; ok {
			dest <- blobref.SizedBlobRef{br, size}
		}
	}
	return nil
}

func (sr *statsStatReceiver) DumpStats(histo string) {
	sr.lock()
	defer sr.mu.Unlock()

	var sum int64
	for _, size := range sr.have {
		sum += size
	}
	fmt.Printf("In-memory blob stats: %d blobs, %d bytes\n", len(sr.have), sum)
	if histo != "" {
		sr.bsHisto(histo)
	}
}

func (sr *statsStatReceiver) bsHisto(file string) {
	bars := sr.histo.Bars()
	if bars == nil {
		return
	}
	f, err := os.Create(file)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	for _, hb := range bars {
		fmt.Fprintf(f, "%d	%d\n", hb.Value, hb.Count)
	}
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

// UploadCache is the "stat cache" for regular files.  Given a current
// working directory, possibly relative filename, and stat info,
// returns what the ultimate put result (the top-level "file" schema
// blob) for that regular file was.
type UploadCache interface {
	CachedPutResult(pwd, filename string, fi os.FileInfo) (*client.PutResult, error)
	AddCachedPutResult(pwd, filename string, fi os.FileInfo, pr *client.PutResult)
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
	mode := fi.Mode()
	if mode&os.ModeType == 0 {
		return up.uploadNodeRegularFile(n)
	}
	m := schema.NewCommonFileMap(n.fullPath, fi)
	switch {
	case mode&os.ModeSymlink != 0:
		// TODO(bradfitz): use VFS here; not os.Readlink
		target, err := os.Readlink(n.fullPath)
		if err != nil {
			return nil, err
		}
		m.SetSymlinkTarget(target)
	case mode&os.ModeDevice != 0:
		// including mode & os.ModeCharDevice
		fallthrough
	case mode&os.ModeSocket != 0:
		fallthrough
	case mode&os.ModeNamedPipe != 0: // FIFO
		fallthrough
	default:
		return nil, schema.ErrUnimplemented
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

// statReceiver returns the StatReceiver used for checking for and uploading blobs.
func (up *Uploader) statReceiver() blobserver.StatReceiver {
	statReceiver := up.altStatReceiver
	if statReceiver == nil {
		// TODO(bradfitz): just make Client be a
		// StatReceiver? move remote's ReceiveBlob ->
		// Upload wrapper into Client itself?
		statReceiver = remote.NewFromClient(up.Client)
	}
	return statReceiver
}

// fileWriterFunc returns the file chunking algorithm to use.
func (up *Uploader) fileWriterFunc() func(blobserver.StatReceiver, schema.Map, io.Reader) (*blobref.BlobRef, error) {
	if up.rollSplits {
		return schema.WriteFileMapRolling
	}
	return schema.WriteFileMap
}

func (up *Uploader) uploadNodeRegularFile(n *node) (*client.PutResult, error) {
	m := schema.NewCommonFileMap(n.fullPath, n.fi)
	m["camliType"] = "file"
	file, err := up.open(n.fullPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var fileContents io.Reader = io.LimitReader(file, n.fi.Size())
	if n.wantFilePermanode() {
		fileContents = &trackDigestReader{r: fileContents}
	}
	blobref, err := up.fileWriterFunc()(up.statReceiver(), m, fileContents)
	if err != nil {
		return nil, err
	}

	if n.wantFilePermanode() {
		sum := fileContents.(*trackDigestReader).Sum()
		// Use a fixed time value for signing; not using modtime
		// so two identical files don't have different modtimes?
		// TODO(bradfitz): consider this more?
		permaNodeSigTime := time.Unix(0, 0)
		permaNode, err := up.UploadPlannedPermanode(sum, permaNodeSigTime)
		if err != nil {
			return nil, fmt.Errorf("Error uploading permanode for node %v: %v", n, err)
		}
		handleResult("node-permanode", permaNode, nil)

		// claimTime is both the time of the "claimDate" in the
		// JSON claim, as well as the date in the OpenPGP
		// header.
		// TODO(bradfitz): this is a little clumsy to do by hand.
		// There should probably be a method on *Uploader to do this
		// from an unsigned schema map. Maybe ditch the schema.Claimer
		// type and just have the Uploader override the claimDate.
		claimTime := n.fi.ModTime()

		contentAttr := schema.NewSetAttributeClaim(permaNode.BlobRef, "camliContent", blobref.String())
		contentAttr.SetClaimDate(claimTime)
		signed, err := up.SignMap(contentAttr, claimTime)
		if err != nil {
			return nil, fmt.Errorf("Failed to sign content claim for node %v: %v", n, err)
		}
		put, err := up.uploadString(signed)
		if err != nil {
			return nil, fmt.Errorf("Error uploading permanode's attribute for node %v: %v", n, err)
		}
		handleResult("node-permanode-contentattr", put, nil)
	}

	// TODO(bradfitz): faking a PutResult here to return
	// is kinda gross.  should instead make a
	// blobserver.Storage wrapper type (wrapping
	// statReceiver) that can track some of this?  or make
	// schemaWriteFileMap return it?
	json, _ := m.JSON()
	pr := &client.PutResult{BlobRef: blobref, Size: int64(len(json)), Skipped: false}
	return pr, nil
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

func (n *node) wantFilePermanode() bool {
	return n.tu != nil && n.tu.FilePermanodes
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

	// If FilePermanodes is set true before Start, each
	// file gets its own content-based planned permanode.
	FilePermanodes bool

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
		blobStats := t.up.Stats()
		log.Printf("FILES: Total: %+v Skipped: %+v Uploaded: %+v %s last upload: %s BLOBS: %s",
			t.total, t.skipped, t.uploaded,
			statStatus, lastUpload,
			blobStats.String())
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

type byFileName []os.FileInfo

func (s byFileName) Len() int           { return len(s) }
func (s byFileName) Less(i, j int) bool { return s[i].Name() < s[j].Name() }
func (s byFileName) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

// trackDigestReader is an io.Reader wrapper which records the digest of what it reads.
type trackDigestReader struct {
	r io.Reader
	h hash.Hash
}

func (t *trackDigestReader) Read(p []byte) (n int, err error) {
	if t.h == nil {
		t.h = sha1.New()
	}
	n, err = t.r.Read(p)
	t.h.Write(p[:n])
	return
}

func (t *trackDigestReader) Sum() string {
	return fmt.Sprintf("%x", t.h.Sum(nil))
}
