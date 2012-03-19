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
	"sort"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/blobserver/localdisk" // used for the blob cache
	"camlistore.org/pkg/cacher"
	"camlistore.org/pkg/client"
	"camlistore.org/pkg/fs"

	"camlistore.org/third_party/code.google.com/p/rsc/fuse"
)

func PrintMap(m map[string]float64) {
	keys := make([]string, len(m))
	for k, _ := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)
	for _, k := range keys {
		if m[k] > 0 {
			fmt.Println(k, m[k])
		}
	}
}

func main() {
	// Scans the arg list and sets up flags
	debug := flag.Bool("debug", false, "print debugging messages.")
	client.AddFlags()
	flag.Parse()

	errorf := func(msg string, args ...interface{}) {
		fmt.Fprintf(os.Stderr, msg, args...)
		os.Exit(2)
	}

	if flag.NArg() < 2 {
		errorf("usage: cammount <blobref> <mountpoint>\n")
	}

	root := blobref.Parse(flag.Arg(0))
	if root == nil {
		errorf("Error parsing root blobref: %q\n", root)
	}
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

	fs := fs.NewCamliFileSystem(fetcher, root)
	if *debug {
		// TODO: set fs's logger
	}

	mountPoint := flag.Arg(1)

	conn, err := fuse.Mount(mountPoint)
	if err != nil {
		log.Fatalf("Mount: %v", err)
	}
	err = conn.Serve(fs)
	if err != nil {
		log.Fatalf("Serve: %", err)
	}
	log.Printf("fuse process ending.")
}
