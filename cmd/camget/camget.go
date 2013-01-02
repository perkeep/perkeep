// The camget tool fetches blobs, files, and directories.
//
// Examples
//
// Writes to stdout by default:
//
//   camget <blobref>                 // dump raw blob
//   camget -contents <file-blobref>  // dump file contents
//
// Like curl, lets you set output file/directory with -o:
//
//   camget -o <dir> <blobref>
//     (if <dir> exists and is directory, <blobref> must be a directory;
//      use -f to overwrite any files)
//
//   camget -o <filename> <file-blobref>
//
// TODO(bradfitz): camget isn't very fleshed out. In general, using 'cammount' to just
// mount a tree is an easier way to get files back.
package main

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

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/client"
	"camlistore.org/pkg/index"
	"camlistore.org/pkg/schema"
)

var (
	flagVerbose  = flag.Bool("verbose", false, "be verbose")
	flagCheck    = flag.Bool("check", false, "just check for the existence of listed blobs; returning 0 if all are present")
	flagOutput   = flag.String("o", "-", "Output file/directory to create.  Use -f to overwrite.")
	flagVia      = flag.String("via", "", "Fetch the blob via the given comma-separated sharerefs (dev only).")
	flagGraph    = flag.Bool("graph", false, "Output a graphviz directed graph .dot file of the provided root schema blob, to be rendered with 'dot -Tsvg -o graph.svg graph.dot'")
	flagContents = flag.Bool("contents", false, "If true and the target blobref is a 'bytes' or 'file' schema blob, the contents of that file are output instead.")
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

	if *flagGraph && flag.NArg() != 1 {
		log.Fatalf("The --graph option requires exactly one parameter.")
	}

	cl := client.NewOrFail()

	for n := 0; n < flag.NArg(); n++ {
		arg := flag.Arg(n)
		br := blobref.Parse(arg)
		if br == nil {
			log.Fatalf("Failed to parse argument %q as a blobref.", arg)
		}
		if *flagGraph {
			printGraph(cl, br)
			return
		}
		if *flagCheck {
			// TODO: do HEAD requests checking if the blobs exists.
			log.Fatal("not implemented")
			return
		}
		if *flagOutput == "-" {
			var rc io.ReadCloser
			var err error
			if *flagContents {
				seekFetcher := blobref.SeekerFromStreamingFetcher(cl)
				rc, err = schema.NewFileReader(seekFetcher, br)
			} else {
				rc, err = fetch(cl, br)
			}
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

func fetch(cl *client.Client, br *blobref.BlobRef) (r io.ReadCloser, err error) {
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

// A little less than the sniffer will take, so we don't truncate.
const sniffSize = 900 * 1024

// smartFetch the things that blobs point to, not just blobs.
func smartFetch(cl *client.Client, targ string, br *blobref.BlobRef) error {
	rc, err := fetch(cl, br)
	if err != nil {
		return err
	}
	defer rc.Close()

	sniffer := new(index.BlobSniffer)
	_, err = io.CopyN(sniffer, rc, sniffSize)
	if err != nil && err != io.EOF {
		return err
	}

	sniffer.Parse()
	sc, ok := sniffer.Superset()

	if !ok {
		if *flagVerbose {
			log.Printf("Fetching opaque data %v into %q", br, targ)
		}

		// opaque data - put it in a file
		f, err := os.Create(targ)
		if err != nil {
			return fmt.Errorf("opaque: %v", err)
		}
		defer f.Close()
		body, _ := sniffer.Body()
		r := io.MultiReader(bytes.NewReader(body), rc)
		_, err = io.Copy(f, r)
		return err
	}

	sc.BlobRef = br

	switch sc.Type {
	case "directory":
		dir := filepath.Join(targ, sc.FileName)
		if *flagVerbose {
			log.Printf("Fetching directory %v into %s", br, dir)
		}
		if err := os.MkdirAll(dir, sc.FileMode()); err != nil {
			return err
		}
		if err := setFileMeta(dir, sc); err != nil {
			log.Print(err)
		}
		entries := blobref.Parse(sc.Entries)
		if entries == nil {
			return fmt.Errorf("bad entries blobref: %v", sc.Entries)
		}
		return smartFetch(cl, dir, entries)
	case "static-set":
		if *flagVerbose {
			log.Printf("Fetching directory entries %v into %s", br, targ)
		}

		// directory entries
		for _, m := range sc.Members {
			// TODO: do n at a time
			dref := blobref.Parse(m)
			if dref == nil {
				return fmt.Errorf("bad member blobref: %v", m)
			}
			if err := smartFetch(cl, targ, dref); err != nil {
				return err
			}
		}
		return nil
	case "file":
		seekFetcher := blobref.SeekerFromStreamingFetcher(cl)
		fr, err := schema.NewFileReader(seekFetcher, br)
		if err != nil {
			return fmt.Errorf("NewFileReader: %v", err)
		}
		defer fr.Close()

		name := filepath.Join(targ, sc.FileName)

		if fi, err := os.Stat(name); err == nil && fi.Size() == fi.Size() {
			if *flagVerbose {
				log.Printf("Skipping %s; already exists.", name)
				return nil
			}
		}

		if *flagVerbose {
			log.Printf("Writing %s to %s ...", br, name)
		}

		f, err := os.Create(name)
		if err != nil {
			return fmt.Errorf("file type: %v", err)
		}
		defer f.Close()
		if _, err := io.Copy(f, fr); err != nil {
			return fmt.Errorf("Copying %s to %s: %v", br, name, err)
		}
		if err := setFileMeta(name, sc); err != nil {
			log.Print(err)
		}
		return nil
	default:
		return errors.New("unknown blob type: " + sc.Type)
	}
	panic("unreachable")
}

func setFileMeta(name string, sc *schema.Superset) error {
	err1 := os.Chmod(name, sc.FileMode())
	var err2 error
	if mt := sc.ModTime(); !mt.IsZero() {
		err2 = os.Chtimes(name, mt, mt)
	}
	err3 := os.Chown(name, sc.UnixOwnerId, sc.UnixGroupId)
	// Return first non-nil error for logging.
	for _, err := range []error{err1, err2, err3} {
		if err != nil {
			return err
		}
	}
	return nil
}
