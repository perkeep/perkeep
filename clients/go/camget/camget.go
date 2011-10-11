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
	"flag"
	"io"
	"log"
	"os"
	"strings"

	"camli/blobref"
	"camli/client"
)

var (
	flagVerbose = flag.Bool("verbose", false, "be verbose")
	flagCheck   = flag.Bool("check", false, "just check for the existence of listed blobs; returning 0 if all our present")
	flagOutput  = flag.String("o", "-", "Output file/directory to create.  Use -f to overwrite.")
	flagVia     = flag.String("via", "", "Fetch the blob via the given comma-separated sharerefs (dev only).")
)

func main() {
	client.AddFlags()
	flag.Parse()

	client := client.NewOrFail()
	if *flagCheck {
		// Simply do HEAD requests checking if the blobs exists.
		return
	}

	var w io.Writer = os.Stdout

	for n := 0; n < flag.NArg(); n++ {
		arg := flag.Arg(n)
		br := blobref.Parse(arg)
		if br == nil {
			log.Fatalf("Failed to parse argument \"%s\" as a blobref.", arg)
		}
		if *flagVerbose {
			log.Printf("Need to fetch %s", br.String())
		}
		var (
			r   io.ReadCloser
			err os.Error
		)

		if len(*flagVia) > 0 {
			vs := strings.Split(*flagVia, ",")
			abr := make([]*blobref.BlobRef, len(vs))
			for i, sbr := range vs {
				abr[i] = blobref.Parse(sbr)
				if abr[i] == nil {
					log.Fatalf("Invalid -via blobref: %q", sbr)
				}
				if *flagVerbose {
					log.Printf("via: %s", sbr)
				}
			}
			r, _, err = client.FetchVia(br, abr)
		} else {
			r, _, err = client.FetchStreaming(br)
		}
		if err != nil {
			log.Fatalf("Failed to fetch %q: %s", br, err)
		}
		defer r.Close()
		_, err = io.Copy(w, r)
		if err != nil {
			log.Fatalf("Failed transferring %q: %s", br, err)
		}
	}

}
