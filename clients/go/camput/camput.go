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
	"flag"
	"fmt"
	"http"
	"io"
	"log"
	"os"
	"sort"

	"camli/blobref"
	"camli/client"
	"camli/schema"
	"camli/jsonsign"
)

const buffered = 16 // arbitrary

// Things that can be uploaded.  (at most one of these)
//var flagTransitive = flag.Bool("transitive", true, "share the transitive closure of the given blobrefs")
//var flagRemove = flag.Bool("remove", false, "remove the list of blobrefs")

var (
	flagVerbose  = flag.Bool("verbose", false, "be verbose")
	flagCacheLog = flag.Bool("logcache", false, "log caching details")
)

var (
	flagUseStatCache = flag.Bool("statcache", false, "Use the stat cache, assuming unchanged files already uploaded in the past are still there. Fast, but potentially dangerous.")
	flagUseHaveCache = flag.Bool("havecache", false, "Use the 'have cache', a cache keeping track of what blobs the remote server should already have from previous uploads.")
)

//var flagSetAttr = flag.Bool("set-attr", false, "set (replace) an attribute")
//var flagAddAttr = flag.Bool("add-attr", false, "add an attribute, additional if one already exists")

var ErrUsage = os.NewError("invalid command usage")

type CommandRunner interface {
	Usage()
	RunCommand(up *Uploader, args []string) os.Error
}

var modeCommands = make(map[string]CommandRunner)

func RegisterCommand(mode string, cmd CommandRunner) {
	if _, dup := modeCommands[mode]; dup {
		log.Fatalf("duplicate command %q registered", mode)
	}
	modeCommands[mode] = cmd
}

// wereErrors gets set to true if any error was encountered, which
// changes the os.Exit value
var wereErrors = false

// UploadCache is the "stat cache" for regular files.  Given a current
// working directory, possibly relative filename, and stat info,
// returns what the ultimate put result (the top-level "file" schema
// blob) for that regular file was.
type UploadCache interface {
	CachedPutResult(pwd, filename string, fi *os.FileInfo) (*client.PutResult, os.Error)
	AddCachedPutResult(pwd, filename string, fi *os.FileInfo, pr *client.PutResult)
}

type HaveCache interface {
	BlobExists(br *blobref.BlobRef) bool
	NoteBlobExists(br *blobref.BlobRef)
}

type Uploader struct {
	*client.Client
	entityFetcher jsonsign.EntityFetcher

	transport *tinkerTransport
	pwd       string
	statCache UploadCache
	haveCache HaveCache

	filecapc chan bool
}

func blobDetails(contents io.ReadSeeker) (bref *blobref.BlobRef, size int64, err os.Error) {
	s1 := sha1.New()
	contents.Seek(0, 0)
	size, err = io.Copy(s1, contents)
	if err == nil {
		bref = blobref.FromHash("sha1", s1)
	}
	return
}

func (up *Uploader) UploadFileBlob(filename string) (*client.PutResult, os.Error) {
	var (
		err  os.Error
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
		// assuming what I did here is not too lame, maybe I should set a limit on how much we accept from the stdin?
		file := buf.Bytes()
		s1 := sha1.New()
		size, err = io.Copy(s1, buf)
		if err != nil {
			return nil, err
		}
		ref = blobref.FromHash("sha1", s1)
		body = io.LimitReader(bytes.NewBuffer(file), size)
	} else {
		fi, err := os.Stat(filename)
		if err != nil {
			return nil, err
		}
		if !fi.IsRegular() {
			return nil, fmt.Errorf("%q is not a regular file", filename)
		}
		file, err := os.Open(filename)
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

func (up *Uploader) UploadFile(filename string) (respr *client.PutResult, outerr os.Error) {
	up.getUploadToken()
	defer up.releaseUploadToken()

	fi, err := os.Lstat(filename)
	if err != nil {
		return nil, err
	}

	if up.statCache != nil && fi.IsRegular() {
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

	switch {
	case fi.IsRegular():
		// Put the blob of the file itself.  (TODO: smart boundary chunking)
		// For now we just store it as one range.
		blobpr, err := up.UploadFileBlob(filename)
		if err != nil {
			return nil, err
		}
		parts := []schema.BytesPart{{BlobRef: blobpr.BlobRef, Size: uint64(blobpr.Size)}}
		if blobpr.Size != fi.Size {
			// TODO: handle races of file changing while reading it
			// after the stat.
		}
		m["camliType"] = "file"
		if err = schema.PopulateParts(m, fi.Size, parts); err != nil {
			return nil, err
		}
	case fi.IsSymlink():
		if err = schema.PopulateSymlinkMap(m, filename); err != nil {
			return nil, err
		}
	case fi.IsDirectory():
		ss := new(schema.StaticSet)
		dir, err := os.Open(filename)
		if err != nil {
			return nil, err
		}
		dirNames, err := dir.Readdirnames(-1)
		if err != nil {
			return nil, err
		}
		dir.Close()
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
			err    os.Error
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
		var entUploadErr os.Error
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
	case fi.IsBlock():
		fallthrough
	case fi.IsChar():
		fallthrough
	case fi.IsSocket():
		fallthrough
	case fi.IsFifo():
		fallthrough
	default:
		return nil, schema.ErrUnimplemented
	}

	mappr, err := up.UploadMap(m)
	if err == nil {
		vlog.Printf("Uploaded %q, %s for %s", m["camliType"], mappr.BlobRef, filename)
	} else {
		vlog.Printf("Error uploading map %v: %v", m, err)
	}
	return mappr, err
}

func (up *Uploader) SignMap(m map[string]interface{}) (string, os.Error) {
	camliSigBlobref := up.Client.SignerPublicKeyBlobref()
	if camliSigBlobref == nil {
		// TODO: more helpful error message
		return "", os.NewError("No public key configured.")
	}

	m["camliSigner"] = camliSigBlobref.String()
	unsigned, err := schema.MapToCamliJson(m)
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

func (up *Uploader) UploadMap(m map[string]interface{}) (*client.PutResult, os.Error) {
	json, err := schema.MapToCamliJson(m)
	if err != nil {
		return nil, err
	}
	return up.uploadString(json)
}

func (up *Uploader) UploadAndSignMap(m map[string]interface{}) (*client.PutResult, os.Error) {
	signed, err := up.SignMap(m)
	if err != nil {
		return nil, err
	}
	return up.uploadString(signed)
}

func (up *Uploader) uploadString(s string) (*client.PutResult, os.Error) {
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

func (up *Uploader) UploadNewPermanode() (*client.PutResult, os.Error) {
	unsigned := schema.NewUnsignedPermanode()
	return up.UploadAndSignMap(unsigned)
}

func (up *Uploader) UploadShare(target *blobref.BlobRef, transitive bool) (*client.PutResult, os.Error) {
	unsigned := schema.NewShareRef(schema.ShareHaveRef, target, transitive)
	return up.UploadAndSignMap(unsigned)
}

func sumSet(flags ...*bool) (count int) {
	for _, f := range flags {
		if *f {
			count++
		}
	}
	return
}

func usage(msg string) {
	if msg != "" {
		fmt.Println("Error:", msg)
	}
	fmt.Println(`
Usage: camput [globalopts] <mode> [commandopts] [commandargs]

Examples:

  camput init

  camput file [opts] <files/directories>

  camput permanode [opts] (create a new permanode)

  camput share [opts] <blobref to share via haveref>

  camput blob <files>     (raw, without any metadata)
  camput blob -           (read from stdin)

  camput attr <permanode> <name> <value>         Set attribute
  camput attr --add <permanode> <name> <value>   Adds attribute (e.g. "tag")
  camput attr --del <permanode> <name> [<value>] Deletes named attribute [value]

For mode-specific help:

  camput MODE -help

Global options:
`)
	flag.PrintDefaults()
	os.Exit(1)
}

func handleResult(what string, pr *client.PutResult, err os.Error) {
	if err != nil {
		log.Printf("Error putting %s: %s", what, err)
		wereErrors = true
		return
	}
	fmt.Println(pr.BlobRef.String())
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
		filecapc:  make(chan bool, 10 /* TODO: config option on max files at a time */ ),
		entityFetcher: &jsonsign.CachingEntityFetcher{
			Fetcher: &jsonsign.FileEntityFetcher{File: cc.SecretRingFile()},
		},
	}

	if *flagUseStatCache {
		cache := NewFlatStatCache()
		defer cache.Save()
		up.statCache = cache
	}
	if *flagUseHaveCache {
		cache := NewFlatHaveCache()
		defer cache.Save()
		up.haveCache = cache
	}
	return up
}

func main() {
	jsonsign.AddFlags()
	flag.Parse()

	if flag.NArg() == 0 {
		usage("No mode given.")
	}

	mode := flag.Arg(0)
	cmd, ok := modeCommands[mode]
	if !ok {
		usage(fmt.Sprintf("Unknown mode %q", mode))
	}

	up := makeUploader()
	err := cmd.RunCommand(up, flag.Args()[1:])
	if err == ErrUsage {
		cmd.Usage()
		os.Exit(1)
	}
	if *flagVerbose {
		stats := up.Stats()
		log.Printf("Client stats: %s", stats.String())
		log.Printf("  #HTTP reqs: %d", up.transport.reqs)
	}
	if err != nil || wereErrors /* TODO: remove this part */ {
		log.Printf("Error: %v", err)
		os.Exit(2)
	}
}

// TODO(bradfitz): finish converting these to the new style

/*
 case *flagShare:
 if flag.NArg() != 1 {
 log.Fatalf("--share only supports one blobref")
 }
 br := blobref.Parse(flag.Arg(0))
 if br == nil {
 log.Fatalf("BlobRef is invalid: %q", flag.Arg(0))
 }
 pr, err := up.UploadShare(br, *flagTransitive)
 handleResult("share", pr, err)
 case *flagRemove:
 if flag.NArg() == 0 {
 log.Fatalf("--remove takes one or more blobrefs")
 }
 err := up.RemoveBlobs(blobref.ParseMulti(flag.Args()))
 if err != nil {
			log.Printf("Error removing blobs %s: %s", strings.Join(flag.Args(), ","), err)
			wereErrors = true
		}

*/
