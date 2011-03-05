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
	errorCount := 0
	go func() {
		srcErr <- sc.EnumerateBlobs(srcBlobs)
	}()
	if *flagDest == "stdout" {
		for sb := range srcBlobs {
			fmt.Printf("%s %d\n", sb.BlobRef, sb.Size)
		}
	} else {
		go func() {
			destErr <- dc.EnumerateBlobs(destBlobs)
		}()

		// Merge sort srcBlobs and destBlobs
		destNotHaveBlobs := make(chan *blobref.SizedBlobRef, 100)
		go client.ListMissingDestinationBlobs(destNotHaveBlobs, srcBlobs, destBlobs)
		for sb := range destNotHaveBlobs {
			fmt.Printf("Destination needs blob: %s\n", sb)

			blobReader, size, err := sc.Fetch(sb.BlobRef)
			if err != nil {
				errorCount++
				log.Printf("Error fetching %s: %v", sb.BlobRef, err)
				continue
			}
			if size != sb.Size {
				errorCount++
				log.Printf("Source blobserver's enumerate size of %d for blob %s doesn't match its Get size of %d",
					sb.Size, sb.BlobRef, size)
				continue
			}
			uh := &client.UploadHandle{BlobRef: sb.BlobRef, Size: size, Contents: blobReader}
			pr, err := dc.Upload(uh)
			if err != nil {
				errorCount++
				log.Printf("Upload of %s to destination blobserver failed: %v", sb.BlobRef, err)
				continue
			}
			log.Printf("Put: %v", pr)
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
