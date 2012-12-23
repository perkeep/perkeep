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
	"os"
	"time"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/client"
)

var (
	flagLoop    = flag.Bool("loop", false, "sync in a loop once done; requires --removesrc")
	flagVerbose = flag.Bool("verbose", false, "be verbose")

	flagSrc      = flag.String("src", "", "Source blobserver prefix (generally a mirrored queue partition)")
	flagSrcPass  = flag.String("srcpassword", "", "Source password")
	flagDest     = flag.String("dest", "", "Destination blobserver, or 'stdout' to just enumerate the --src blobs to stdout")
	flagDestPass = flag.String("destpassword", "", "Destination password")

	flagRemoveSource = flag.Bool("removesrc", false,
		"remove each blob from the source after syncing to the destination; for queue processing")
)

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

	// TODO(mpl): adapt to the userpass scheme once it has settled
	sc := client.New(*flagSrc)
	sc.SetupAuth()
	dc := client.New(*flagDest)
	dc.SetupAuth()

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
		if *flagVerbose {
			log.Printf("sync stats - pass: %d, blobs: %d, bytes %d\n", passNum, stats.BlobsCopied, stats.BytesCopied)
		}
		if err != nil {
			log.Fatalf("sync failed: %v", err)
		}
		if !*flagLoop {
			break
		}
	}
}

func doPass(sc, dc *client.Client, passNum int) (stats SyncStats, retErr error) {
	srcBlobs := make(chan blobref.SizedBlobRef, 100)
	destBlobs := make(chan blobref.SizedBlobRef, 100)
	srcErr := make(chan error)
	destErr := make(chan error)

	go func() {
		srcErr <- sc.SimpleEnumerateBlobs(srcBlobs)
	}()
	checkSourceError := func() {
		if err := <-srcErr; err != nil {
			retErr = fmt.Errorf("Enumerate error from source: %v", err)
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
		destErr <- dc.SimpleEnumerateBlobs(destBlobs)
	}()
	checkDestError := func() {
		if err := <-destErr; err != nil {
			retErr = errors.New(fmt.Sprintf("Enumerate error from destination: %v", err))
		}
	}

	destNotHaveBlobs := make(chan blobref.SizedBlobRef)
	sizeMismatch := make(chan *blobref.BlobRef)
	readSrcBlobs := srcBlobs
	if *flagVerbose {
		readSrcBlobs = loggingBlobRefChannel(srcBlobs)
	}
	mismatches := []*blobref.BlobRef{}
	go client.ListMissingDestinationBlobs(destNotHaveBlobs, sizeMismatch, readSrcBlobs, destBlobs)
For:
	for {
		select {
		case br := <-sizeMismatch:
			// TODO(bradfitz): check both sides and repair, carefully.  For now, fail.
			log.Printf("WARNING: blobref %v has differing sizes on source and est", br)
			stats.ErrorCount++
			mismatches = append(mismatches, br)
		case sb, ok := <-destNotHaveBlobs:
			if !ok {
				break For
			}
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
	}

	checkSourceError()
	checkDestError()
	if retErr == nil && stats.ErrorCount > 0 {
		retErr = errors.New(fmt.Sprintf("%d errors during sync", stats.ErrorCount))
	}
	return stats, retErr
}

func loggingBlobRefChannel(ch <-chan blobref.SizedBlobRef) chan blobref.SizedBlobRef {
	ch2 := make(chan blobref.SizedBlobRef)
	go func() {
		defer close(ch2)
		var last time.Time
		var nblob, nbyte int64
		for v := range ch {
			ch2 <- v
			nblob++
			nbyte += v.Size
			now := time.Now()
			if last.IsZero() || now.After(last.Add(1*time.Second)) {
				last = now
				log.Printf("At source blob %v (%d blobs, %d bytes)", v.BlobRef, nblob, nbyte)
			}
		}
		log.Printf("Total blobs: %d, %d bytes", nblob, nbyte)
	}()
	return ch2
}
