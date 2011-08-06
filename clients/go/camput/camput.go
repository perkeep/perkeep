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
	"io"
	"log"
	"os"
	"sort"
	"strings"

	"camli/blobref"
	"camli/client"
	"camli/schema"
	"camli/jsonsign"
)

// Things that can be uploaded.  (at most one of these)
var flagBlob = flag.Bool("blob", false, "upload a file's bytes as a single blob")
var flagFile = flag.Bool("file", false, "upload a file's bytes as a blob, as well as its JSON file record")
var flagPermanode = flag.Bool("permanode", false, "create a new permanode")
var flagInit = flag.Bool("init", false, "first-time configuration.")
var flagShare = flag.Bool("share", false, "create a camli share by haveref with the given blobrefs")
var flagTransitive = flag.Bool("transitive", true, "share the transitive closure of the given blobrefs")
var flagRemove = flag.Bool("remove", false, "remove the list of blobrefs")
var flagName = flag.String("name", "", "Optional name attribute to set on permanode when using -permanode and -file")
var flagTag = flag.String("tag", "", "Optional tag attribute to set on permanode when using -permanode and -file")
var flagVerbose = flag.Bool("verbose", false, "be verbose")

var flagSetAttr = flag.Bool("set-attr", false, "set (replace) an attribute")
var flagAddAttr = flag.Bool("add-attr", false, "add an attribute, additional if one already exists")

var flagSplits = flag.Bool("debug-splits", false, "show splits")

var wereErrors = false

type Uploader struct {
	*client.Client
	entityFetcher jsonsign.EntityFetcher
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
	if *flagVerbose {
		log.Printf("Uploading filename: %s", filename)
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}

	ref, size, err := blobDetails(file)
	if err != nil {
		return nil, err
	}
	file.Seek(0, 0)
	handle := &client.UploadHandle{ref, size, file}
	return up.Upload(handle)
}

func (up *Uploader) UploadFile(filename string) (*client.PutResult, os.Error) {
	fi, err := os.Lstat(filename)
	if err != nil {
		return nil, err
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
		parts := []schema.ContentPart{{BlobRef: blobpr.BlobRef, Size: uint64(blobpr.Size)}}
		if blobpr.Size != fi.Size {
			// TODO: handle races of file changing while reading it
			// after the stat.
		}
		if err = schema.PopulateRegularFileMap(m, fi.Size, parts); err != nil {
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
		// TODO: process dirName entries in parallel
		for _, dirEntName := range dirNames {
			pr, err := up.UploadFile(filename + "/" + dirEntName)
			if err != nil {
				return nil, err
			}
			ss.Add(pr.BlobRef)
		}
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
	return mappr, err
}

func (up *Uploader) UploadMap(m map[string]interface{}) (*client.PutResult, os.Error) {
	json, err := schema.MapToCamliJson(m)
	if err != nil {
		return nil, err
	}
	if *flagVerbose {
		fmt.Printf("json: %s\n", json)
	}
	return up.Upload(client.NewUploadHandleFromString(json))
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

func (up *Uploader) UploadAndSignMap(m map[string]interface{}) (*client.PutResult, os.Error) {
	signed, err := up.SignMap(m)
	if err != nil {
		return nil, err
	}
	return up.Upload(client.NewUploadHandleFromString(signed))
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
Usage: camput

  camput --init       # first time configuration
  camput --blob <filename(s) to upload as blobs>
  camput --file <filename(s) to upload as blobs + JSON metadata>
  camput --share <blobref to share via haveref> [--transitive]
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
	if *flagVerbose {
		fmt.Printf("Put %s: %q\n", what, pr)
	} else {
		fmt.Println(pr.BlobRef.String())
	}
}

func main() {
	jsonsign.AddFlags()
	flag.Parse()

	if *flagSplits {
		showSplits()
		return
	}

	nOpts := sumSet(flagFile, flagBlob, flagPermanode, flagInit, flagShare, flagRemove,
		flagSetAttr, flagAddAttr)
	if !(nOpts == 1 ||
		(nOpts == 2 && *flagFile && *flagPermanode)) {
		usage("Conflicting mode options.")
	}

	cc := client.NewOrFail()
	if !*flagVerbose {
		cc.SetLogger(nil)
	}
	up := &Uploader{
		Client: cc,
		entityFetcher: &jsonsign.CachingEntityFetcher{
			Fetcher: &jsonsign.FileEntityFetcher{File: cc.SecretRingFile()},
		},
	}
	switch {
	case *flagInit:
		doInit()
		return
	case *flagFile || *flagBlob:
		var (
			permaNode *client.PutResult
			lastPut   *client.PutResult
			err       os.Error
		)
		if n := flag.NArg(); *flagPermanode {
			if n != 1 {
				log.Fatalf("Options --permanode and --file can only be used together when there's exactly one argument")
			}
			permaNode, err = up.UploadNewPermanode()
			if err != nil {
				log.Fatalf("Error uploading permanode: %v", err)
			}
		}
		for n := 0; n < flag.NArg(); n++ {
			if *flagBlob {
				lastPut, err = up.UploadFileBlob(flag.Arg(n))
				handleResult("blob", lastPut, err)
			} else {
				lastPut, err = up.UploadFile(flag.Arg(n))
				handleResult("file", lastPut, err)
			}
		}
		if permaNode != nil {
			put, err := up.UploadAndSignMap(schema.NewSetAttributeClaim(permaNode.BlobRef, "camliContent", lastPut.BlobRef.String()))
			handleResult("claim-permanode-content", put, err)
			if *flagName != "" {
				put, err := up.UploadAndSignMap(schema.NewSetAttributeClaim(permaNode.BlobRef, "name", *flagName))
				handleResult("claim-permanode-name", put, err)
			}
			if *flagTag != "" {
				put, err := up.UploadAndSignMap(schema.NewSetAttributeClaim(permaNode.BlobRef, "camliTag", *flagTag))
				handleResult("claim-permanode-tag", put, err)
			}
			handleResult("permanode", permaNode, nil)
		}
	case *flagPermanode:
		if flag.NArg() > 0 {
			log.Fatalf("--permanode doesn't take any additional arguments")
		}
		pr, err := up.UploadNewPermanode()
		handleResult("permanode", pr, err)
		if *flagName != "" {
			put, err := up.UploadAndSignMap(schema.NewSetAttributeClaim(pr.BlobRef, "name", *flagName))
			handleResult("permanode-name", put, err)
		}
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
	case *flagAddAttr || *flagSetAttr:
		if flag.NArg() != 3 {
			log.Fatalf("--set-attr and --add-attr take 3 args: <permanode> <attr> <value>")
		}
		pn := blobref.Parse(flag.Arg(0))
		if pn == nil {
			log.Fatalf("Error parsing blobref %q", flag.Arg(0))
		}
		m := schema.NewSetAttributeClaim(pn, flag.Arg(1), flag.Arg(2))
		if *flagAddAttr {
			m = schema.NewAddAttributeClaim(pn, flag.Arg(1), flag.Arg(2))
		}
		put, err := up.UploadAndSignMap(m)
		handleResult(m["claimType"].(string), put, err)
	}

	if *flagVerbose {
		stats := up.Stats()
		log.Printf("Client stats: %s", stats.String())
	}
	if wereErrors {
		os.Exit(2)
	}
}
