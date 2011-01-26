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
	"camli/blobref"
	"camli/client"
	"flag"
	"io"
	"log"
	"os"
	"strings"
)

var flagVerbose *bool = flag.Bool("verbose", false, "be verbose")

var flagCheck *bool = flag.Bool("check", false, "just check for the existence of listed blobs; returning 0 if all our present")
var flagOutput *string = flag.String("o", "-", "Output file/directory to create.  Use -f to overwrite.")
var flagVia *string = flag.String("via", "", "Fetch the blob via the given comma-separated sharerefs (dev only).")

func main() {
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
			log.Exitf("Failed to parse argument \"%s\" as a blobref.", arg)
		}
		if *flagVerbose {
			log.Printf("Need to fetch %s", br.String())
		}
		var (
			r blobref.ReadSeekCloser
			err os.Error
		)

		if len(*flagVia) > 0 {
			vs := strings.Split(*flagVia, ",", -1)
			abr := make([]*blobref.BlobRef, len(vs))
			for i, sbr := range vs {
				abr[i] = blobref.Parse(sbr)
				if abr[i] == nil {
					log.Exitf("Invalid -via blobref: %q", sbr)
				}
				if *flagVerbose {
					log.Printf("via: %s", sbr)
				}
			}
			r, _, err = client.FetchVia(br, abr)
		} else {
			r, _, err = client.Fetch(br)
		}
		if err != nil {
			log.Exitf("Failed to fetch %q: %s", br, err)
		}
		_, err = io.Copy(w, r)
		if err != nil {
			log.Exitf("Failed transferring %q: %s", br, err)
		}
	}

}
