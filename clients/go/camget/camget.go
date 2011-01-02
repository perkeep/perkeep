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
	"log"
)

var flagVerbose *bool = flag.Bool("verbose", false, "be verbose")

var flagCheck *bool = flag.Bool("check", false, "just check for the existence of listed blobs; returning 0 if all our present")
var flagOutput *string = flag.String("o", "-", "Output file/directory to create.  Use -f to overwrite.")

func main() {
	flag.Parse()

	client := client.NewOrFail()
	if *flagCheck {
		// Simply do HEAD requests checking if the blobs exists.
		return
	}

	for n := 0; n < flag.NArg(); n++ {
		arg := flag.Arg(n)
		blobref := blobref.Parse(arg)
		if blobref == nil {
			log.Exitf("Failed to parse argument \"%s\" as a blobref.", arg)
		}
		if *flagVerbose {
			log.Printf("Need to fetch %s", blobref.String())
		}
	}

	flagOutput = flagOutput
	client = client
}
