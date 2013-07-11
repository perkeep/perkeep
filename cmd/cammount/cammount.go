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
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
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
	debug        = flag.Bool("debug", false, "print debugging messages.")
	mountInChild = flag.Bool("mount_in_child", false, "Run the cammount in a child process, so the top process can catch SIGINT and such easier. Hack to work around OS X FUSE support.")
)

func usage() {
	fmt.Fprint(os.Stderr, "usage: cammount [opts] <mountpoint> [<root-blobref>|<share URL>]\n")
	flag.PrintDefaults()
	os.Exit(2)
}

func main() {
	// Scans the arg list and sets up flags
	client.AddFlags()
	flag.Parse()

	narg := flag.NArg()
	if narg < 1 || narg > 2 {
		usage()
	}

	mountPoint := flag.Arg(0)

	// TODO(bradfitz): this is not reliable yet.
	if *mountInChild {
		log.Printf("Running cammount in child process.")
		cmd := exec.Command(os.Args[0], flag.Args()...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			log.Fatalf("Error running child cammount: %v", err)
		}
		log.Printf("cammount started; awaiting shutdown signals in parent.")

		sigc := make(chan os.Signal, 1)
		go func() {
			var buf [1]byte
			for {
				os.Stdin.Read(buf[:])
				if buf[0] == 'q' {
					break
				}
			}
			log.Printf("Read 'q' from stdin; shutting down.")
			sigc <- syscall.SIGUSR2
		}()
		waitc := make(chan error, 1)
		go func() {
			waitc <- cmd.Wait()
		}()
		signal.Notify(sigc, syscall.SIGQUIT, syscall.SIGTERM)

		sig := <-sigc
		go os.Stat(filepath.Join(mountPoint, ".quitquitquit"))
		log.Printf("Signal %s received, shutting down.", sig)
		select {
		case <-time.After(500 * time.Millisecond):
			cmd.Process.Kill()
		case <-waitc:
		}
		if runtime.GOOS == "darwin" {
			donec := make(chan bool, 1)
			go func() {
				defer close(donec)
				exec.Command("diskutil", "umount", "force", mountPoint).Run()
				log.Printf("Unmounted")
			}()
			select {
			case <-time.After(500 * time.Millisecond):
				log.Printf("Unmount timeout.")
			case <-donec:
			}
		}
		os.Exit(0)
		return
	}

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
		if err.Error() == "cannot find load_fusefs" && runtime.GOOS == "darwin" {
			log.Fatal("FUSE not available; install from http://osxfuse.github.io/")
		}
		log.Fatalf("Mount: %v", err)
	}
	err = conn.Serve(camfs)
	if err != nil {
		log.Fatalf("Serve: %v", err)
	}
	log.Printf("fuse process ending.")
}
