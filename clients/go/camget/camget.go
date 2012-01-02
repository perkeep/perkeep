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

// Usage:
//
// Writes to stdout by default:
//   camget BLOBREF
//
// Like curl, lets you set output file/directory with -o:
//   camget -o dir BLOBREF     (if dir exists and is directory, BLOBREF must be a directory, and -f to overwrite any files)
//   camget -o file  BLOBREF   
//
// Should be possible to get a directory JSON blob without recursively
// fetching an entire directory.  Likewise with files.  But default
// should be sensitive on the type of the listed blob.  Maybe --blob
// just to get the blob?  Seems consistent.

package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"camli/blobref"
	"camli/client"
	"camli/index"
)

var (
	flagVerbose = flag.Bool("verbose", false, "be verbose")
	flagCheck   = flag.Bool("check", false, "just check for the existence of listed blobs; returning 0 if all our present")
	flagOutput  = flag.String("o", "-", "Output file/directory to create.  Use -f to overwrite.")
	flagVia     = flag.String("via", "", "Fetch the blob via the given comma-separated sharerefs (dev only).")
)

var viaRefs []*blobref.BlobRef

func main() {
	client.AddFlags()
	flag.Parse()

	if len(*flagVia) > 0 {
		vs := strings.Split(*flagVia, ",")
		viaRefs = make([]*blobref.BlobRef, len(vs))
		for i, sbr := range vs {
			viaRefs[i] = blobref.Parse(sbr)
			if viaRefs[i] == nil {
				log.Fatalf("Invalid -via blobref: %q", sbr)
			}
			if *flagVerbose {
				log.Printf("via: %s", sbr)
			}
		}
	}

	cl := client.NewOrFail()

	for n := 0; n < flag.NArg(); n++ {
		arg := flag.Arg(n)
		br := blobref.Parse(arg)
		if br == nil {
			log.Fatalf("Failed to parse argument %q as a blobref.", arg)
		}
		if *flagCheck {
			// TODO: do HEAD requests checking if the blobs exists.
			log.Fatal("not implemented")
			return
		}
		if *flagOutput == "-" {
			rc, err := fetch(cl, br)
			if err != nil {
				log.Fatal(err)
			}
			defer rc.Close()
			if _, err := io.Copy(os.Stdout, rc); err != nil {
				log.Fatalf("Failed reading %q: %v", br, err)
			}
			return
		}
		if err := smartFetch(cl, *flagOutput, br); err != nil {
			log.Fatal(err)
		}
	}
}

func fetch(cl *client.Client, br *blobref.BlobRef) (r io.ReadCloser, err os.Error) {
	if *flagVerbose {
		log.Printf("Fetching %s", br.String())
	}
	if len(viaRefs) > 0 {
		r, _, err = cl.FetchVia(br, viaRefs)
	} else {
		r, _, err = cl.FetchStreaming(br)
	}
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch %q: %s", br, err)
	}
	return r, err
}

const sniffSize = 10 * 1024

// smartFetch the things that blobs point to, not just blobs. (wow)
func smartFetch(cl *client.Client, out string, br *blobref.BlobRef) os.Error {
	rc, err := fetch(cl, br)
	if err != nil {
		return err
	}
	defer rc.Close()

	sniffer := new(index.BlobSniffer)
	_, err = io.Copyn(sniffer, rc, sniffSize)
	if err != nil && err != os.EOF {
		return err
	}

	sniffer.Parse()
	_, ok := sniffer.Superset()

	if !ok {
		// opaque data - put it in a file
		f, err := os.Create(out)
		if err != nil {
			return err
		}
		defer f.Close()
		body, _ := sniffer.Body()
		r := io.MultiReader(bytes.NewBuffer(body), rc)
		_, err = io.Copy(f, r)
		return err
	}

	log.Fatal("camli blob parsing not implemented")

	return nil
}
