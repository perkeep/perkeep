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
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/cacher"
	"camlistore.org/pkg/client"
	"camlistore.org/pkg/fs"
	"camlistore.org/third_party/code.google.com/p/rsc/fuse"
)

var (
	debug = flag.Bool("debug", false, "print debugging messages.")
	xterm = flag.Bool("xterm", false, "Run an xterm in the mounted directory. Shut down when xterm ends.")
)

func usage() {
	fmt.Fprint(os.Stderr, "usage: cammount [opts] <mountpoint> [<root-blobref>|<share URL>]\n")
	flag.PrintDefaults()
	os.Exit(2)
}

func main() {
	var conn *fuse.Conn

	// Scans the arg list and sets up flags
	client.AddFlags()
	flag.Parse()

	narg := flag.NArg()
	if narg < 1 || narg > 2 {
		usage()
	}

	mountPoint := flag.Arg(0)

	errorf := func(msg string, args ...interface{}) {
		fmt.Fprintf(os.Stderr, msg, args...)
		fmt.Fprint(os.Stderr, "\n")
		usage()
	}

	var (
		cl    *client.Client
		root  *blobref.BlobRef // nil if only one arg
		camfs *fs.CamliFileSystem
	)
	if narg == 2 {
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
			cl.SetHTTPClient(&http.Client{Transport: cl.TransportForConfig(nil)})
		}
	} else {
		cl = client.NewOrFail() // automatic from flags
		cl.SetHTTPClient(&http.Client{Transport: cl.TransportForConfig(nil)})
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
	}

	if *debug {
		fuse.Debugf = log.Printf
		// TODO: set fs's logger
	}

	// This doesn't appear to work on OS X:
	sigc := make(chan os.Signal, 1)

	conn, err = fuse.Mount(mountPoint)
	if err != nil {
		if err.Error() == "cannot find load_fusefs" && runtime.GOOS == "darwin" {
			log.Fatal("FUSE not available; install from http://osxfuse.github.io/")
		}
		log.Fatalf("Mount: %v", err)
	}

	xtermDone := make(chan bool, 1)
	if *xterm {
		cmd := exec.Command("xterm")
		cmd.Dir = mountPoint
		if err := cmd.Start(); err != nil {
			log.Printf("Error starting xterm: %v", err)
		} else {
			go func() {
				cmd.Wait()
				xtermDone <- true
			}()
			defer cmd.Process.Kill()
		}
	}

	signal.Notify(sigc, syscall.SIGQUIT, syscall.SIGTERM)

	doneServe := make(chan error, 1)
	go func() {
		doneServe <- conn.Serve(camfs)
	}()

	quitKey := make(chan bool, 1)
	go awaitQuitKey(quitKey)

	select {
	case err := <-doneServe:
		log.Printf("conn.Serve returned %v", err)
	case sig := <-sigc:
		log.Printf("Signal %s received, shutting down.", sig)
	case <-quitKey:
		log.Printf("Quit key pressed. Shutting down.")
	case <-xtermDone:
		log.Printf("xterm done")
	}

	time.AfterFunc(2*time.Second, func() {
		os.Exit(1)
	})
	log.Printf("Unmounting...")
	err = unmount(mountPoint)
	log.Printf("Unmount = %v", err)

	log.Printf("cammount FUSE processending.")
}

func awaitQuitKey(done chan<- bool) {
	var buf [1]byte
	for {
		_, err := os.Stdin.Read(buf[:])
		if err != nil {
			return
		}
		if buf[0] == 'q' {
			done <- true
			return
		}
	}
}

func unmount(point string) error {
	if runtime.GOOS == "darwin" {
		errc := make(chan error, 1)
		go func() {
			errc <- exec.Command("diskutil", "umount", "force", point).Run()
		}()
		select {
		case <-time.After(1 * time.Second):
			return errors.New("unmount timeout")
		case err := <-errc:
			log.Printf("diskutil unmount = %v", err)
			return err
		}
	}
	return errors.New("unmount: unimplemented")
}
