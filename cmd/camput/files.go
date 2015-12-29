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
	"bufio"
	"crypto/sha1"
	"errors"
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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"camlistore.org/internal/chanworker"
	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	statspkg "camlistore.org/pkg/blobserver/stats"
	"camlistore.org/pkg/client"
	"camlistore.org/pkg/client/android"
	"camlistore.org/pkg/cmdmain"
	"camlistore.org/pkg/schema"
)

type fileCmd struct {
	title string
	tag   string

	makePermanode     bool // make new, unique permanode of the root (dir or file)
	filePermanodes    bool // make planned permanodes for each file (based on their digest)
	vivify            bool
	exifTime          bool // use metadata (such as in EXIF) to find the creation time of the file
	capCtime          bool // use mtime as creation time of the file, if it would be bigger than modification time
	diskUsage         bool // show "du" disk usage only (dry run mode), don't actually upload
	argsFromInput     bool // Android mode: filenames piped into stdin, one at a time.
	deleteAfterUpload bool // with fileNodes, deletes the input file once uploaded
	contentsOnly      bool // do not store any of the file's attributes, only its contents.

	statcache bool

	// Go into in-memory stats mode only; doesn't actually upload.
	memstats bool
	histo    string // optional histogram output filename
}

var flagUseSQLiteChildCache bool // Use sqlite for the statcache and havecache.

var (
	uploadWorkers    = 5 // concurrent upload workers (negative means unbounded: memory hog)
	statCacheWorkers = 5 // concurrent statcache workers
)

func init() {
	cmdmain.RegisterCommand("file", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(fileCmd)
		flags.BoolVar(&cmd.makePermanode, "permanode", false, "Create an associate a new permanode for the uploaded file or directory.")
		flags.BoolVar(&cmd.filePermanodes, "filenodes", false, "Create (if necessary) content-based permanodes for each uploaded file.")
		flags.BoolVar(&cmd.deleteAfterUpload, "delete_after_upload", false, "If using -filenodes, deletes files once they're uploaded, or if they've already been uploaded.")
		flags.BoolVar(&cmd.vivify, "vivify", false,
			"If true, ask the server to create and sign permanode(s) associated with each uploaded"+
				" file. This permits the server to have your signing key. Used mostly with untrusted"+
				" or at-risk clients, such as phones.")
		flags.BoolVar(&cmd.exifTime, "exiftime", false, "Try to use metadata (such as EXIF) to get a stable creation time. If found, used as the replacement for the modtime. Mainly useful with vivify or filenodes.")
		flags.StringVar(&cmd.title, "title", "", "Optional title attribute to set on permanode when using -permanode.")
		flags.StringVar(&cmd.tag, "tag", "", "Optional tag(s) to set on permanode when using -permanode or -filenodes. Single value or comma separated.")

		flags.BoolVar(&cmd.diskUsage, "du", false, "Dry run mode: only show disk usage information, without upload or statting dest. Used for testing skipDirs configs, mostly.")

		if debug, _ := strconv.ParseBool(os.Getenv("CAMLI_DEBUG")); debug {
			flags.BoolVar(&cmd.statcache, "statcache", true, "(debug flag) Use the stat cache, assuming unchanged files already uploaded in the past are still there. Fast, but potentially dangerous.")
			flags.BoolVar(&cmd.memstats, "debug-memstats", false, "(debug flag) Enter debug in-memory mode; collecting stats only. Doesn't upload anything.")
			flags.StringVar(&cmd.histo, "debug-histogram-file", "", "(debug flag) Optional file to create and write the blob size for each file uploaded.  For use with GNU R and hist(read.table(\"filename\")$V1). Requires debug-memstats.")
			flags.BoolVar(&cmd.capCtime, "capctime", false, "(debug flag) For file blobs use file modification time as creation time if it would be bigger (newer) than modification time. For stable filenode creation (you can forge mtime, but can't forge ctime).")
			flags.BoolVar(&flagUseSQLiteChildCache, "sqlitecache", false, "(debug flag) Use sqlite for the statcache and havecache instead of a flat cache.")
			flags.BoolVar(&cmd.contentsOnly, "contents_only", false, "(debug flag) Do not store any of the file's attributes. We write only the file's contents (the blobRefs for its parts) to the created file schema.")
		} else {
			cmd.statcache = true
		}
		if android.IsChild() {
			flags.BoolVar(&cmd.argsFromInput, "stdinargs", false, "If true, filenames to upload are sent one-per-line on stdin. EOF means to quit the process with exit status 0.")
			// limit number of goroutines to limit memory
			uploadWorkers = 2
			statCacheWorkers = 2
		}
		flagCacheLog = flags.Bool("logcache", false, "log caching details")

		return cmd
	})
}

func (c *fileCmd) Describe() string {
	return "Upload file(s)."
}

func (c *fileCmd) Usage() {
	fmt.Fprintf(cmdmain.Stderr, "Usage: camput [globalopts] file [fileopts] <file/director(ies)>\n")
}

func (c *fileCmd) Examples() []string {
	return []string{
		"[opts] <file(s)/director(ies)",
		"--permanode --title='Homedir backup' --tag=backup,homedir $HOME",
		"--filenodes /mnt/camera/DCIM",
	}
}

func (c *fileCmd) RunCommand(args []string) error {
	if c.vivify {
		if c.makePermanode || c.filePermanodes || c.tag != "" || c.title != "" {
			return cmdmain.UsageError("--vivify excludes any other option")
		}
	}
	if c.title != "" && !c.makePermanode {
		return cmdmain.UsageError("Can't set title without using --permanode")
	}
	if c.tag != "" && !c.makePermanode && !c.filePermanodes {
		return cmdmain.UsageError("Can't set tag without using --permanode or --filenodes")
	}
	if c.histo != "" && !c.memstats {
		return cmdmain.UsageError("Can't use histo without memstats")
	}
	if c.deleteAfterUpload && !c.filePermanodes {
		return cmdmain.UsageError("Can't set use --delete_after_upload without --filenodes")
	}
	if c.filePermanodes && c.contentsOnly {
		return cmdmain.UsageError("--contents_only and --filenodes are exclusive. Use --permanode instead.")
	}
	// TODO(mpl): do it for other modes too. Or even better, do it once for all modes.
	if *cmdmain.FlagVerbose {
		log.SetOutput(cmdmain.Stderr)
	} else {
		log.SetOutput(ioutil.Discard)
	}
	up := getUploader()
	if c.memstats {
		sr := new(statspkg.Receiver)
		up.altStatReceiver = sr
		defer func() { DumpStats(sr, c.histo) }()
	}
	c.initCaches(up)

	if c.makePermanode || c.filePermanodes {
		testSigBlobRef := up.Client.SignerPublicKeyBlobref()
		if !testSigBlobRef.Valid() {
			return cmdmain.UsageError("A GPG key is needed to create permanodes; configure one or use vivify mode.")
		}
	}
	up.fileOpts = &fileOptions{
		permanode:    c.filePermanodes,
		tag:          c.tag,
		vivify:       c.vivify,
		exifTime:     c.exifTime,
		capCtime:     c.capCtime,
		contentsOnly: c.contentsOnly,
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
		t.Start()
		pr, err := t.Wait()
		if err != nil {
			return err
		}
		handleResult("tree-upload", pr, err)
		return nil
	}
	if c.argsFromInput {
		if len(args) > 0 {
			return errors.New("args not supported with -argsfrominput")
		}
		tu := up.NewRootlessTreeUpload()
		tu.Start()
		br := bufio.NewReader(os.Stdin)
		for {
			path, err := br.ReadString('\n')
			if path = strings.TrimSpace(path); path != "" {
				tu.Enqueue(path)
			}
			if err == io.EOF {
				android.PreExit()
				os.Exit(0)
			}
			if err != nil {
				log.Fatal(err)
			}
		}
	}

	if len(args) == 0 {
		return cmdmain.UsageError("No files or directories given.")
	}
	if up.statCache != nil {
		defer up.statCache.Close()
	}
	for _, filename := range args {
		fi, err := os.Stat(filename)
		if err != nil {
			return err
		}
		// Skip ignored files or base directories.  Failing to skip the
		// latter results in a panic.
		if up.Client.IsIgnoredFile(filename) {
			log.Printf("Client configured to ignore %s; skipping.", filename)
			continue
		}
		if fi.IsDir() {
			if up.fileOpts.wantVivify() {
				vlog.Printf("Directories not supported in vivify mode; skipping %v\n", filename)
				continue
			}
			t := up.NewTreeUpload(filename)
			t.Start()
			lastPut, err = t.Wait()
		} else {
			lastPut, err = up.UploadFile(filename)
			if err == nil && c.deleteAfterUpload {
				if err := os.Remove(filename); err != nil {
					log.Printf("Error deleting %v: %v", filename, err)
				} else {
					log.Printf("Deleted %v", filename)
				}
			}
		}
		if handleResult("file", lastPut, err) != nil {
			return err
		}
	}

	if permaNode != nil && lastPut != nil {
		put, err := up.UploadAndSignBlob(schema.NewSetAttributeClaim(permaNode.BlobRef, "camliContent", lastPut.BlobRef.String()))
		if handleResult("claim-permanode-content", put, err) != nil {
			return err
		}
		if c.title != "" {
			put, err := up.UploadAndSignBlob(schema.NewSetAttributeClaim(permaNode.BlobRef, "title", c.title))
			handleResult("claim-permanode-title", put, err)
		}
		if c.tag != "" {
			tags := strings.Split(c.tag, ",")
			for _, tag := range tags {
				m := schema.NewAddAttributeClaim(permaNode.BlobRef, "tag", tag)
				put, err := up.UploadAndSignBlob(m)
				handleResult("claim-permanode-tag", put, err)
			}
		}
		handleResult("permanode", permaNode, nil)
	}
	return nil
}

func (c *fileCmd) initCaches(up *Uploader) {
	if !c.statcache || *flagBlobDir != "" {
		return
	}
	gen, err := up.StorageGeneration()
	if err != nil {
		log.Printf("WARNING: not using local caches; failed to retrieve server's storage generation: %v", err)
		return
	}
	if c.statcache {
		up.statCache = NewKvStatCache(gen)
	}
}

// DumpStats creates the destFile and writes a line per received blob,
// with its blob size.
func DumpStats(sr *statspkg.Receiver, destFile string) {
	sr.Lock()
	defer sr.Unlock()

	f, err := os.Create(destFile)
	if err != nil {
		log.Fatal(err)
	}

	var sum int64
	for _, size := range sr.Have {
		fmt.Fprintf(f, "%d\n", size)
	}
	fmt.Printf("In-memory blob stats: %d blobs, %d bytes\n", len(sr.Have), sum)

	err = f.Close()
	if err != nil {
		log.Fatal(err)
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

func (n *node) directoryStaticSet() (*schema.StaticSet, error) {
	ss := new(schema.StaticSet)
	for _, c := range n.children {
		pr, err := c.PutResult()
		if err != nil {
			return nil, fmt.Errorf("Error populating directory static set for child %q: %v", c.fullPath, err)
		}
		ss.Add(pr.BlobRef)
	}
	return ss, nil
}

func (up *Uploader) uploadNode(n *node) (*client.PutResult, error) {
	fi := n.fi
	mode := fi.Mode()
	if mode&os.ModeType == 0 {
		return up.uploadNodeRegularFile(n)
	}
	bb := schema.NewCommonFileMap(n.fullPath, fi)
	switch {
	case mode&os.ModeSymlink != 0:
		// TODO(bradfitz): use VFS here; not os.Readlink
		target, err := os.Readlink(n.fullPath)
		if err != nil {
			return nil, err
		}
		bb.SetSymlinkTarget(target)
	case mode&os.ModeDevice != 0:
		// including mode & os.ModeCharDevice
		fallthrough
	case mode&os.ModeSocket != 0:
		bb.SetType("socket")
	case mode&os.ModeNamedPipe != 0: // fifo
		bb.SetType("fifo")
	default:
		return nil, fmt.Errorf("camput.files: unsupported file type %v for file %v", mode, n.fullPath)
	case fi.IsDir():
		ss, err := n.directoryStaticSet()
		if err != nil {
			return nil, err
		}
		sspr, err := up.UploadBlob(ss)
		if err != nil {
			return nil, err
		}
		bb.PopulateDirectoryMap(sspr.BlobRef)
	}

	mappr, err := up.UploadBlob(bb)
	if err == nil {
		if !mappr.Skipped {
			vlog.Printf("Uploaded %q, %s for %s", bb.Type(), mappr.BlobRef, n.fullPath)
		}
	} else {
		vlog.Printf("Error uploading map for %s (%s, %s): %v", n.fullPath, bb.Type(), bb.Blob().BlobRef(), err)
	}
	return mappr, err

}

// statReceiver returns the StatReceiver used for checking for and uploading blobs.
//
// The optional provided node is only used for conditionally printing out status info to stdout.
func (up *Uploader) statReceiver(n *node) blobserver.StatReceiver {
	statReceiver := up.altStatReceiver
	if statReceiver == nil {
		// TODO(mpl): simplify the altStatReceiver situation as well,
		// see TODO in cmd/camput/uploader.go
		statReceiver = up.Client
	}
	if android.IsChild() && n != nil && n.fi.Mode()&os.ModeType == 0 {
		return android.StatusReceiver{Sr: statReceiver, Path: n.fullPath}
	}
	return statReceiver
}

func (up *Uploader) noStatReceiver(r blobserver.BlobReceiver) blobserver.StatReceiver {
	return noStatReceiver{r}
}

// A haveCacheStatReceiver relays Receive calls to the embedded
// BlobReceiver and treats all Stat calls like the blob doesn't exist.
//
// This is used by the client once it's already asked the server that
// it doesn't have the whole file in some chunk layout already, so we
// know we're just writing new stuff. For resuming in the middle of
// larger uploads, it turns out that the pkg/client.Client.Upload
// already checks the have cache anyway, so going right to mid-chunk
// receives is fine.
//
// TODO(bradfitz): this probabaly all needs an audit/rationalization/tests
// to make sure all the players are agreeing on the responsibilities.
// And maybe the Android stats are wrong, too. (see pkg/client/android's
// StatReceiver)
type noStatReceiver struct {
	blobserver.BlobReceiver
}

func (noStatReceiver) StatBlobs(dest chan<- blob.SizedRef, blobs []blob.Ref) error {
	return nil
}

var atomicDigestOps int64 // number of files digested

// wholeFileDigest returns the sha1 digest of the regular file's absolute
// path given in fullPath.
func (up *Uploader) wholeFileDigest(fullPath string) (blob.Ref, error) {
	// TODO(bradfitz): cache this.
	file, err := up.open(fullPath)
	if err != nil {
		return blob.Ref{}, err
	}
	defer file.Close()
	td := &trackDigestReader{r: file}
	_, err = io.Copy(ioutil.Discard, td)
	atomic.AddInt64(&atomicDigestOps, 1)
	if err != nil {
		return blob.Ref{}, err
	}
	return blob.MustParse(td.Sum()), nil
}

var noDupSearch, _ = strconv.ParseBool(os.Getenv("CAMLI_NO_FILE_DUP_SEARCH"))

// fileMapFromDuplicate queries the server's search interface for an
// existing file with an entire contents of sum (a blobref string).
// If the server has it, it's validated, and then fileMap (which must
// already be partially populated) has its "parts" field populated,
// and then fileMap is uploaded (if necessary) and a PutResult with
// its blobref is returned. If there's any problem, or a dup doesn't
// exist, ok is false.
// If required, Vivify is also done here.
func (up *Uploader) fileMapFromDuplicate(bs blobserver.StatReceiver, fileMap *schema.Builder, sum string) (pr *client.PutResult, ok bool) {
	if noDupSearch {
		return
	}
	_, err := up.Client.SearchRoot()
	if err != nil {
		return
	}
	dupFileRef, err := up.Client.SearchExistingFileSchema(blob.MustParse(sum))
	if err != nil {
		log.Printf("Warning: error searching for already-uploaded copy of %s: %v", sum, err)
		return nil, false
	}
	if !dupFileRef.Valid() {
		return nil, false
	}
	if *cmdmain.FlagVerbose {
		log.Printf("Found dup of contents %s in file schema %s", sum, dupFileRef)
	}
	dupMap, err := up.Client.FetchSchemaBlob(dupFileRef)
	if err != nil {
		log.Printf("Warning: error fetching %v: %v", dupFileRef, err)
		return nil, false
	}

	fileMap.PopulateParts(dupMap.PartsSize(), dupMap.ByteParts())

	json, err := fileMap.JSON()
	if err != nil {
		return nil, false
	}
	uh := client.NewUploadHandleFromString(json)
	if up.fileOpts.wantVivify() {
		uh.Vivify = true
	}
	if !uh.Vivify && uh.BlobRef == dupFileRef {
		// Unchanged (same filename, modtime, JSON serialization, etc)
		return &client.PutResult{BlobRef: dupFileRef, Size: uint32(len(json)), Skipped: true}, true
	}
	pr, err = up.Upload(uh)
	if err != nil {
		log.Printf("Warning: error uploading file map after finding server dup of %v: %v", sum, err)
		return nil, false
	}
	return pr, true
}

func (up *Uploader) uploadNodeRegularFile(n *node) (*client.PutResult, error) {
	var filebb *schema.Builder
	if up.fileOpts.contentsOnly {
		filebb = schema.NewFileMap("")
	} else {
		filebb = schema.NewCommonFileMap(n.fullPath, n.fi)
	}
	filebb.SetType("file")

	up.fdGate.Start()
	defer up.fdGate.Done()

	file, err := up.open(n.fullPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if !up.fileOpts.contentsOnly {
		if up.fileOpts.exifTime {
			ra, ok := file.(io.ReaderAt)
			if !ok {
				return nil, errors.New("Error asserting local file to io.ReaderAt")
			}
			modtime, err := schema.FileTime(ra)
			if err != nil {
				log.Printf("warning: getting time from EXIF failed for %v: %v", n.fullPath, err)
			} else {
				filebb.SetModTime(modtime)
			}
		}
		if up.fileOpts.capCtime {
			filebb.CapCreationTime()
		}
	}

	var (
		size                           = n.fi.Size()
		fileContents io.Reader         = io.LimitReader(file, size)
		br           blob.Ref          // of file schemaref
		sum          string            // sha1 hashsum of the file to upload
		pr           *client.PutResult // of the final "file" schema blob
	)

	const dupCheckThreshold = 256 << 10
	if size > dupCheckThreshold {
		sumRef, err := up.wholeFileDigest(n.fullPath)
		if err == nil {
			sum = sumRef.String()
			ok := false
			pr, ok = up.fileMapFromDuplicate(up.statReceiver(n), filebb, sum)
			if ok {
				br = pr.BlobRef
				android.NoteFileUploaded(n.fullPath, !pr.Skipped)
				if up.fileOpts.wantVivify() {
					// we can return early in that case, because the other options
					// are disallowed in the vivify case.
					return pr, nil
				}
			}
		}
	}

	if up.fileOpts.wantVivify() {
		// If vivify wasn't already done in fileMapFromDuplicate.
		err := schema.WriteFileChunks(up.noStatReceiver(up.statReceiver(n)), filebb, fileContents)
		if err != nil {
			return nil, err
		}
		json, err := filebb.JSON()
		if err != nil {
			return nil, err
		}
		br = blob.SHA1FromString(json)
		h := &client.UploadHandle{
			BlobRef:  br,
			Size:     uint32(len(json)),
			Contents: strings.NewReader(json),
			Vivify:   true,
		}
		pr, err = up.Upload(h)
		if err != nil {
			return nil, err
		}
		android.NoteFileUploaded(n.fullPath, true)
		return pr, nil
	}

	if !br.Valid() {
		// br still zero means fileMapFromDuplicate did not find the file on the server,
		// and the file has not just been uploaded subsequently to a vivify request.
		// So we do the full file + file schema upload here.
		if sum == "" && up.fileOpts.wantFilePermanode() {
			fileContents = &trackDigestReader{r: fileContents}
		}
		br, err = schema.WriteFileMap(up.noStatReceiver(up.statReceiver(n)), filebb, fileContents)
		if err != nil {
			return nil, err
		}
	}

	// The work for those planned permanodes (and the claims) is redone
	// everytime we get here (i.e past the stat cache). However, they're
	// caught by the have cache, so they won't be reuploaded for nothing
	// at least.
	if up.fileOpts.wantFilePermanode() {
		if td, ok := fileContents.(*trackDigestReader); ok {
			sum = td.Sum()
		}
		// claimTime is both the time of the "claimDate" in the
		// JSON claim, as well as the date in the OpenPGP
		// header.
		// TODO(bradfitz): this is a little clumsy to do by hand.
		// There should probably be a method on *Uploader to do this
		// from an unsigned schema map. Maybe ditch the schema.Claimer
		// type and just have the Uploader override the claimDate.
		claimTime, ok := filebb.ModTime()
		if !ok {
			return nil, fmt.Errorf("couldn't get modtime for file %v", n.fullPath)
		}
		err = up.uploadFilePermanode(sum, br, claimTime)
		if err != nil {
			return nil, fmt.Errorf("Error uploading permanode for node %v: %v", n, err)
		}
	}

	// TODO(bradfitz): faking a PutResult here to return
	// is kinda gross.  should instead make a
	// blobserver.Storage wrapper type (wrapping
	// statReceiver) that can track some of this?  or make
	// schemaWriteFileMap return it?
	json, _ := filebb.JSON()
	pr = &client.PutResult{BlobRef: br, Size: uint32(len(json)), Skipped: false}
	return pr, nil
}

// uploadFilePermanode creates and uploads the planned permanode (with sum as a
// fixed key) associated with the file blobref fileRef.
// It also sets the optional tags for this permanode.
func (up *Uploader) uploadFilePermanode(sum string, fileRef blob.Ref, claimTime time.Time) error {
	// Use a fixed time value for signing; not using modtime
	// so two identical files don't have different modtimes?
	// TODO(bradfitz): consider this more?
	permaNodeSigTime := time.Unix(0, 0)
	permaNode, err := up.UploadPlannedPermanode(sum, permaNodeSigTime)
	if err != nil {
		return fmt.Errorf("Error uploading planned permanode: %v", err)
	}
	handleResult("node-permanode", permaNode, nil)

	contentAttr := schema.NewSetAttributeClaim(permaNode.BlobRef, "camliContent", fileRef.String())
	contentAttr.SetClaimDate(claimTime)
	signer, err := up.Signer()
	if err != nil {
		return err
	}
	signed, err := contentAttr.SignAt(signer, claimTime)
	if err != nil {
		return fmt.Errorf("Failed to sign content claim: %v", err)
	}
	put, err := up.uploadString(signed)
	if err != nil {
		return fmt.Errorf("Error uploading permanode's attribute: %v", err)
	}

	handleResult("node-permanode-contentattr", put, nil)
	if tags := up.fileOpts.tags(); len(tags) > 0 {
		errch := make(chan error)
		for _, tag := range tags {
			go func(tag string) {
				m := schema.NewAddAttributeClaim(permaNode.BlobRef, "tag", tag)
				m.SetClaimDate(claimTime)
				signed, err := m.SignAt(signer, claimTime)
				if err != nil {
					errch <- fmt.Errorf("Failed to sign tag claim: %v", err)
					return
				}
				put, err := up.uploadString(signed)
				if err != nil {
					errch <- fmt.Errorf("Error uploading permanode's tag attribute %v: %v", tag, err)
					return
				}
				handleResult("node-permanode-tag", put, nil)
				errch <- nil
			}(tag)
		}

		for _ = range tags {
			if e := <-errch; e != nil && err == nil {
				err = e
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (up *Uploader) UploadFile(filename string) (*client.PutResult, error) {
	fullPath, err := filepath.Abs(filename)
	if err != nil {
		return nil, err
	}
	fi, err := up.lstat(fullPath)
	if err != nil {
		return nil, err
	}

	if fi.IsDir() {
		panic("must use UploadTree now for directories")
	}
	n := &node{
		fullPath: fullPath,
		fi:       fi,
	}

	withPermanode := up.fileOpts.wantFilePermanode()
	if up.statCache != nil && !up.fileOpts.wantVivify() {
		// Note: ignoring cache hits if wantVivify, otherwise
		// a non-vivify put followed by a vivify one wouldn't
		// end up doing the vivify.
		if cachedRes, err := up.statCache.CachedPutResult(
			up.pwd, n.fullPath, n.fi, withPermanode); err == nil {
			return cachedRes, nil
		}
	}

	pr, err := up.uploadNode(n)
	if err == nil && up.statCache != nil {
		up.statCache.AddCachedPutResult(
			up.pwd, n.fullPath, n.fi, pr, withPermanode)
	}

	return pr, err
}

// NewTreeUpload returns a TreeUpload. It doesn't begin uploading any files until a
// call to Start
func (up *Uploader) NewTreeUpload(dir string) *TreeUpload {
	tu := up.NewRootlessTreeUpload()
	tu.rootless = false
	tu.base = dir
	return tu
}

func (up *Uploader) NewRootlessTreeUpload() *TreeUpload {
	return &TreeUpload{
		rootless: true,
		base:     "",
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
	if res == nil && err == nil {
		panic("SetPutResult called with (nil, nil)")
	}
	if n.res != nil || n.err != nil {
		panic("SetPutResult called twice on node " + n.fullPath)
	}
	n.res, n.err = res, err
	n.cond.Broadcast()
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

	// Immutable:
	rootless bool   // if true, "base" will be empty.
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

// Enqueue starts uploading path (a file, directory, etc).
func (t *TreeUpload) Enqueue(path string) {
	t.statPath(path, nil)
}

// fi is optional (will be statted if nil)
func (t *TreeUpload) statPath(fullPath string, fi os.FileInfo) (nod *node, err error) {
	defer func() {
		if err == nil && nod != nil {
			t.stattedc <- nod
		}
	}()
	if t.up.Client.IsIgnoredFile(fullPath) {
		return nil, nil
	}
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
	sort.Sort(byTypeAndName(fis))
	for _, fi := range fis {
		depn, err := t.statPath(filepath.Join(fullPath, filepath.Base(fi.Name())), fi)
		if err != nil {
			return nil, err
		}
		if depn != nil {
			n.children = append(n.children, depn)
		}
	}
	return n, nil
}

// testHookStatCache, if non-nil, runs first in the checkStatCache worker.
var testHookStatCache func(el interface{}, ok bool)

func (t *TreeUpload) run() {
	defer close(t.donec)

	// Kick off scanning all files, eventually learning the root
	// node (which references all its children).
	var root *node // nil until received and set in loop below.
	rootc := make(chan *node, 1)
	if !t.rootless {
		go func() {
			n, err := t.statPath(t.base, nil)
			if err != nil {
				log.Fatalf("Error scanning files under %s: %v", t.base, err)
			}
			close(t.stattedc)
			rootc <- n
		}()
	}

	var lastStat, lastUpload string
	dumpStats := func() {
		if android.IsChild() {
			printAndroidCamputStatus(t)
			return
		}
		statStatus := ""
		if root == nil {
			statStatus = fmt.Sprintf("last stat: %s", lastStat)
		}
		blobStats := t.up.Stats()
		log.Printf("FILES: Total: %+v Skipped: %+v Uploaded: %+v %s BLOBS: %s Digested: %d last upload: %s",
			t.total, t.skipped, t.uploaded,
			statStatus,
			blobStats.String(),
			atomic.LoadInt64(&atomicDigestOps),
			lastUpload)
	}

	// Channels for stats & progress bars. These are never closed:
	uploadedc := make(chan *node) // at least tried to upload; server might have had blob
	skippedc := make(chan *node)  // didn't even hit blobserver; trusted our stat cache

	uploadsdonec := make(chan bool)
	var upload chan<- interface{}
	withPermanode := t.up.fileOpts.wantFilePermanode()
	if t.DiskUsageMode {
		upload = chanworker.NewWorker(1, func(el interface{}, ok bool) {
			if !ok {
				uploadsdonec <- true
				return
			}
			n := el.(*node)
			if n.fi.IsDir() {
				fmt.Printf("%d\t%s\n", n.SumBytes()>>10, n.fullPath)
			}
		})
	} else {
		// dirUpload is unbounded because directories can depend on directories.
		// We bound the number of HTTP requests in flight instead.
		// TODO(bradfitz): remove this chanworker stuff?
		dirUpload := chanworker.NewWorker(-1, func(el interface{}, ok bool) {
			if !ok {
				log.Printf("done uploading directories - done with all uploads.")
				uploadsdonec <- true
				return
			}
			n := el.(*node)
			put, err := t.up.uploadNode(n)
			if err != nil {
				log.Fatalf("Error uploading %s: %v", n.fullPath, err)
			}
			n.SetPutResult(put, nil)
			uploadedc <- n
		})

		upload = chanworker.NewWorker(uploadWorkers, func(el interface{}, ok bool) {
			if !ok {
				log.Printf("done with all uploads.")
				close(dirUpload)
				log.Printf("closed dirUpload")
				return
			}
			n := el.(*node)
			if n.fi.IsDir() {
				dirUpload <- n
				return
			}
			put, err := t.up.uploadNode(n)
			if err != nil {
				log.Fatalf("Error uploading %s: %v", n.fullPath, err)
			}
			n.SetPutResult(put, nil)
			if c := t.up.statCache; c != nil {
				c.AddCachedPutResult(
					t.up.pwd, n.fullPath, n.fi, put, withPermanode)
			}
			uploadedc <- n
		})
	}

	checkStatCache := chanworker.NewWorker(statCacheWorkers, func(el interface{}, ok bool) {
		if hook := testHookStatCache; hook != nil {
			hook(el, ok)
		}
		if !ok {
			if t.up.statCache != nil {
				log.Printf("done checking stat cache")
			}
			close(upload)
			return
		}
		n := el.(*node)
		if t.DiskUsageMode || t.up.statCache == nil {
			upload <- n
			return
		}
		if !n.fi.IsDir() {
			cachedRes, err := t.up.statCache.CachedPutResult(
				t.up.pwd, n.fullPath, n.fi, withPermanode)
			if err == nil {
				n.SetPutResult(cachedRes, nil)
				cachelog.Printf("Cache HIT on %q -> %v", n.fullPath, cachedRes)
				android.NoteFileUploaded(n.fullPath, false)
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
				log.Printf("done statting:")
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

type byTypeAndName []os.FileInfo

func (s byTypeAndName) Len() int { return len(s) }
func (s byTypeAndName) Less(i, j int) bool {
	// files go before directories
	if s[i].IsDir() {
		if !s[j].IsDir() {
			return false
		}
	} else if s[j].IsDir() {
		return true
	}
	return s[i].Name() < s[j].Name()
}
func (s byTypeAndName) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

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
	return fmt.Sprintf("sha1-%x", t.h.Sum(nil))
}
