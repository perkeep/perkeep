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
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/blobserver/localdisk" // used for the blob cache
	"camlistore.org/pkg/cacher"
	"camlistore.org/pkg/client"
	"camlistore.org/pkg/fs"

	"camlistore.org/third_party/code.google.com/p/rsc/fuse"
)

func main() {
	// Scans the arg list and sets up flags
	debug := flag.Bool("debug", false, "print debugging messages.")
	client.AddFlags()
	flag.Parse()

	errorf := func(msg string, args ...interface{}) {
		fmt.Fprintf(os.Stderr, msg, args...)
		os.Exit(2)
	}

	if n := flag.NArg(); n < 1 || n > 2 {
		errorf("usage: cammount <mountpoint> [<root-blobref>]\n")
	}

	mountPoint := flag.Arg(0)

	client := client.NewOrFail() // automatic from flags

	cacheDir, err := ioutil.TempDir("", "camlicache")
	if err != nil {
		errorf("Error creating temp cache directory: %v\n", err)
	}
	defer os.RemoveAll(cacheDir)
	diskcache, err := localdisk.New(cacheDir)
	if err != nil {
		errorf("Error setting up local disk cache: %v", err)
	}
	fetcher := cacher.NewCachingFetcher(diskcache, client)

	var camfs *fs.CamliFileSystem
	if flag.NArg() == 2 {
		root := blobref.Parse(flag.Arg(1))
		if root == nil {
			errorf("Error parsing root blobref: %q\n", root)
		}
		var err error
		camfs, err = fs.NewRootedCamliFileSystem(fetcher, root)
		if err != nil {
			errorf("Error creating root with %v: %v", root, err)
		}
	} else {
		camfs = fs.NewCamliFileSystem(fetcher)
		log.Printf("starting with fs %#v", camfs)
	}

	if *debug {
		// TODO: set fs's logger
	}

	conn, err := fuse.Mount(mountPoint)
	if err != nil {
		log.Fatalf("Mount: %v", err)
	}
	err = conn.Serve(camfs)
	if err != nil {
		log.Fatalf("Serve: %v", err)
	}
	log.Printf("fuse process ending.")
}
