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
	"camli/blobref"
	"camli/client"
	"flag"
	"fmt"
	"log"
	"os"
)

// Things that can be uploaded.  (at most one of these)
var flagLoop = flag.Bool("loop", false, "sync in a loop once done")
var flagVerbose = flag.Bool("verbose", false, "be verbose")

var flagSrc = flag.String("src", "", "Source host")
var flagSrcPass = flag.String("srcpassword", "", "Source password")
var flagDest = flag.String("dest", "", "Destination blobserver, or 'stdout' to just enumerate the --src blobs to stdout")
var flagDestPass = flag.String("destpassword", "", "Destination password")

func usage(err string) {
	if err != "" {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
	}
	flag.PrintDefaults()
	os.Exit(2)
}

// TODO: use Generics if/when available
type chanPeeker struct {
	ch     chan *blobref.SizedBlobRef
	peek   *blobref.SizedBlobRef
	Closed bool
}

func (cp *chanPeeker) Peek() *blobref.SizedBlobRef {
	if cp.Closed {
		return nil
	}
	if cp.peek != nil {
		return cp.peek
	}
	cp.peek = <-cp.ch
	if closed(cp.ch) {
		cp.Closed = true
		return nil
	}
	return cp.peek
}

func (cp *chanPeeker) Take() *blobref.SizedBlobRef {
	v := cp.Peek()
	cp.peek = nil
	return v
}

func yieldMissingDestinationBlobs(destMissing, srcch, dstch chan *blobref.SizedBlobRef) {
	defer close(destMissing)

	src := &chanPeeker{ch: srcch}
	dst := &chanPeeker{ch: dstch}

	for src.Peek() != nil {
		// If the destination has reached its end, anything
		// remaining in the source is needed.
		if dst.Peek() == nil {
			destMissing <- src.Take()
			continue
		}

		srcStr := src.Peek().BlobRef.String()
		dstStr := dst.Peek().BlobRef.String()
		switch {
		case srcStr == dstStr:
			// Skip both
			src.Take()
			dst.Take()
		case srcStr < dstStr:
			src.Take()
		case srcStr > dstStr:
			destMissing <- src.Take()
		}
	}
}

func main() {
	flag.Parse()

	if *flagSrc == "" {
		usage("No srchost specified.")
	}
	if *flagDest == "" {
		usage("No desthost specified.")
	}

	sc := client.New(*flagSrc, *flagSrcPass)
	dc := client.New(*flagDest, *flagDestPass)

	var logger *log.Logger = nil
	if *flagVerbose {
		logger = log.New(os.Stderr, "", 0)
	}
	sc.SetLogger(logger)
	dc.SetLogger(logger)

	srcBlobs := make(chan *blobref.SizedBlobRef, 100)
	destBlobs := make(chan *blobref.SizedBlobRef, 100)
	srcErr := make(chan os.Error)
	destErr := make(chan os.Error)
	go func() {
		srcErr <- sc.EnumerateBlobs(srcBlobs)
	}()
	if *flagDest == "stdout" {
		for sb := range srcBlobs {
			fmt.Printf("%s %d\n", sb.BlobRef, sb.Size)
		}
	} else {
		go func() {
			destErr <- sc.EnumerateBlobs(destBlobs)
		}()

		// Merge sort srcBlobs and destBlobs
		destNotHaveBlobs := make(chan *blobref.SizedBlobRef, 100)
		go yieldMissingDestinationBlobs(destNotHaveBlobs, srcBlobs, destBlobs)
		for sb := range destNotHaveBlobs {
			fmt.Printf("Destination needs blob: %s\n", sb)
		}
	}

	if err := <-srcErr; err != nil {
		log.Fatalf("Enumerate error from source: %v", err)
	}
	if *flagDest != "stdout" {
		if err := <-destErr; err != nil {
			log.Fatalf("Enumerate error from destination: %v", err)
		}
	}
}
