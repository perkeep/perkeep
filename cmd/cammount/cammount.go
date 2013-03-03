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
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/cacher"
	"camlistore.org/pkg/client"
	"camlistore.org/pkg/fs"

	"camlistore.org/third_party/code.google.com/p/rsc/fuse"
)

func usage() {
	fmt.Fprint(os.Stderr, "usage: cammount [opts] <mountpoint> [<root-blobref>|<share URL>]\n")
	flag.PrintDefaults()
	os.Exit(2)
}

func main() {
	// Scans the arg list and sets up flags
	debug := flag.Bool("debug", false, "print debugging messages.")
	client.AddFlags()
	flag.Parse()

	errorf := func(msg string, args ...interface{}) {
		fmt.Fprintf(os.Stderr, msg, args...)
		fmt.Fprint(os.Stderr, "\n")
		usage()
	}

	nargs := flag.NArg()
	if nargs < 1 || nargs > 2 {
		usage()
	}

	mountPoint := flag.Arg(0)
	var (
		cl    *client.Client
		root  *blobref.BlobRef // nil if only one arg
		camfs *fs.CamliFileSystem
	)
	if nargs == 2 {
		rootArg := flag.Arg(1)
		// not trying very hard since NewFromShareRoot will do it better with a regex
		if strings.HasPrefix(rootArg, "http://") ||
			strings.HasPrefix(rootArg, "https://") {
			if client.ExplicitServer() != "" {
				errorf("Can't use an explicit blobserver with a share URL; the blobserver is implicit from the share URL.")
			}
			var err error
			cl, root, err = client.NewFromShareRoot(rootArg)
			if err != nil {
				log.Fatal(err)
			}
		} else {
			cl = client.NewOrFail() // automatic from flags
			root = blobref.Parse(rootArg)
			if root == nil {
				log.Fatalf("Error parsing root blobref: %q\n", rootArg)
			}
		}
	} else {
		cl = client.NewOrFail() // automatic from flags
	}

	diskCacheFetcher, err := cacher.NewDiskCache(cl)
	if err != nil {
		log.Fatalf("Error setting up local disk cache: %v", err)
	}
	defer diskCacheFetcher.Clean()
	if root != nil {
		var err error
		camfs, err = fs.NewRootedCamliFileSystem(diskCacheFetcher, root)
		if err != nil {
			log.Fatalf("Error creating root with %v: %v", root, err)
		}
	} else {
		camfs = fs.NewCamliFileSystem(cl, diskCacheFetcher)
		log.Printf("starting with fs %#v", camfs)
	}

	if *debug {
		// TODO: set fs's logger
	}

	// This doesn't appear to work on OS X:
	sigc := make(chan os.Signal, 1)
	go func() {
		log.Fatalf("Signal %s received, shutting down.", <-sigc)
	}()
	signal.Notify(sigc, syscall.SIGQUIT, syscall.SIGTERM)

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
