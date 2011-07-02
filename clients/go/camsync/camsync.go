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
var flagLoop = flag.Bool("loop", false, "sync in a loop once done; requires --removesrc")
var flagVerbose = flag.Bool("verbose", false, "be verbose")

var flagSrc = flag.String("src", "", "Source blobserver prefix (generally a mirrored queue partition)")
var flagSrcPass = flag.String("srcpassword", "", "Source password")
var flagDest = flag.String("dest", "", "Destination blobserver, or 'stdout' to just enumerate the --src blobs to stdout")
var flagDestPass = flag.String("destpassword", "", "Destination password")

var flagRemoveSource = flag.Bool("removesrc", false,
	"remove each blob from the source after syncing to the destination; for queue processing")

type SyncStats struct {
	BlobsCopied int
	BytesCopied int64
	ErrorCount  int
}

func usage(err string) {
	if err != "" {
		fmt.Fprintf(os.Stderr, "Error: %s\n\nUsage:\n", err)
	}
	flag.PrintDefaults()
	os.Exit(2)
}

func main() {
	flag.Parse()

	if *flagSrc == "" {
		usage("No --src specified.")
	}
	if *flagDest == "" {
		usage("No --dest specified.")
	}
	if *flagLoop && !*flagRemoveSource {
		usage("Can't use --loop without --removesrc")
	}

	sc := client.New(*flagSrc, *flagSrcPass)
	dc := client.New(*flagDest, *flagDestPass)

	var logger *log.Logger = nil
	if *flagVerbose {
		logger = log.New(os.Stderr, "", 0)
	}
	sc.SetLogger(logger)
	dc.SetLogger(logger)

	passNum := 0
	for {
		passNum++
		stats, err := doPass(sc, dc, passNum)
		if err != nil {
			log.Fatalf("sync failed: %v", err)
		}
		if *flagVerbose {
			log.Printf("sync stats - pass: %d, blobs: %d, bytes %d\n", passNum, stats.BlobsCopied, stats.BytesCopied)
		}
		if !*flagLoop {
			break
		}
	}
}

func doPass(sc, dc *client.Client, passNum int) (stats SyncStats, retErr os.Error) {
	srcBlobs := make(chan blobref.SizedBlobRef, 100)
	destBlobs := make(chan blobref.SizedBlobRef, 100)
	srcErr := make(chan os.Error)
	destErr := make(chan os.Error)

	go func() {
		srcErr <- sc.EnumerateBlobs(srcBlobs)
	}()
	checkSourceError := func() {
		if err := <-srcErr; err != nil {
			retErr = os.NewError(fmt.Sprintf("Enumerate error from source: %v", err))
		}
	}

	if *flagDest == "stdout" {
		for sb := range srcBlobs {
			fmt.Printf("%s %d\n", sb.BlobRef, sb.Size)
		}
		checkSourceError()
		return
	}

	go func() {
		destErr <- dc.EnumerateBlobs(destBlobs)
	}()
	checkDestError := func() {
		if err := <-destErr; err != nil {
			retErr = os.NewError(fmt.Sprintf("Enumerate error from destination: %v", err))
		}
	}

	destNotHaveBlobs := make(chan blobref.SizedBlobRef, 100)
	go client.ListMissingDestinationBlobs(destNotHaveBlobs, srcBlobs, destBlobs)
	for sb := range destNotHaveBlobs {
		fmt.Printf("Destination needs blob: %s\n", sb)

		blobReader, size, err := sc.FetchStreaming(sb.BlobRef)
		if err != nil {
			stats.ErrorCount++
			log.Printf("Error fetching %s: %v", sb.BlobRef, err)
			continue
		}
		if size != sb.Size {
			stats.ErrorCount++
			log.Printf("Source blobserver's enumerate size of %d for blob %s doesn't match its Get size of %d",
				sb.Size, sb.BlobRef, size)
			continue
		}
		uh := &client.UploadHandle{BlobRef: sb.BlobRef, Size: size, Contents: blobReader}
		pr, err := dc.Upload(uh)
		if err != nil {
			stats.ErrorCount++
			log.Printf("Upload of %s to destination blobserver failed: %v", sb.BlobRef, err)
			continue
		}
		if !pr.Skipped {
			stats.BlobsCopied++
			stats.BytesCopied += pr.Size
		}
		if *flagRemoveSource {
			if err = sc.RemoveBlob(sb.BlobRef); err != nil {
				stats.ErrorCount++
				log.Printf("Failed to delete %s from source: %v", sb.BlobRef, err)
			}
		}
	}

	checkSourceError()
	checkDestError()
	if retErr == nil && stats.ErrorCount > 0 {
		retErr = os.NewError(fmt.Sprintf("%d errors during sync", stats.ErrorCount))
	}
	return stats, retErr
}
