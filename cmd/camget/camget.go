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
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/buildinfo"
	"camlistore.org/pkg/cacher"
	"camlistore.org/pkg/client"
	"camlistore.org/pkg/cmdmain"
	"camlistore.org/pkg/index"
	"camlistore.org/pkg/osutil"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/types"
)

var (
	// Keeping flagVersion and flagVerbose declared like this, so we don't forget and
	// erroneously redeclare them again in conflict with the cmdmain ones, which is not
	// caught at build time.
	flagVersion       = cmdmain.FlagVersion
	flagVerbose       = cmdmain.FlagVerbose
	flagHTTP          = flag.Bool("verbose_http", false, "show HTTP request summaries")
	flagCheck         = flag.Bool("check", false, "just check for the existence of listed blobs; returning 0 if all are present")
	flagOutput        = flag.String("o", "-", "Output file/directory to create.  Use -f to overwrite.")
	flagGraph         = flag.Bool("graph", false, "Output a graphviz directed graph .dot file of the provided root schema blob, to be rendered with 'dot -Tsvg -o graph.svg graph.dot'")
	flagContents      = flag.Bool("contents", false, "If true and the target blobref is a 'bytes' or 'file' schema blob, the contents of that file are output instead.")
	flagShared        = flag.String("shared", "", "If non-empty, the URL of a \"share\" blob. The URL will be used as the root of future fetches. Only \"haveref\" shares are currently supported.")
	flagTrustedCert   = flag.String("cert", "", "If non-empty, the fingerprint (20 digits lowercase prefix of the SHA256 of the complete certificate) of the TLS certificate we trust for the share URL. Requires --shared.")
	flagInsecureTLS   = flag.Bool("insecure", false, "If set, when using TLS, the server's certificates verification is disabled, and they are not checked against the trustedCerts in the client configuration either.")
	flagSkipIrregular = flag.Bool("skip_irregular", false, "If true, symlinks, device files, and other special file types are skipped.")
)

func main() {
	client.AddFlags()
	flag.Parse()

	if *flagVersion {
		fmt.Fprintf(os.Stderr, "camget version: %s\n", buildinfo.Version())
		return
	}

	if *cmdmain.FlagLegal {
		cmdmain.PrintLicenses()
		return
	}

	if *flagGraph && flag.NArg() != 1 {
		log.Fatalf("The --graph option requires exactly one parameter.")
	}

	var cl *client.Client
	var items []blob.Ref

	optTransportConfig := client.OptionTransportConfig(&client.TransportConfig{
		Verbose: *flagHTTP,
	})

	if *flagShared != "" {
		if client.ExplicitServer() != "" {
			log.Fatal("Can't use --shared with an explicit blobserver; blobserver is implicit from the --shared URL.")
		}
		if flag.NArg() != 0 {
			log.Fatal("No arguments permitted when using --shared")
		}
		cl1, target, err := client.NewFromShareRoot(*flagShared,
			client.OptionInsecure(*flagInsecureTLS),
			client.OptionTrustedCert(*flagTrustedCert),
			optTransportConfig,
		)
		if err != nil {
			log.Fatal(err)
		}
		cl = cl1
		items = append(items, target)
	} else {
		if *flagTrustedCert != "" {
			log.Fatal("Can't use --cert without --shared.")
		}
		cl = client.NewOrFail(client.OptionInsecure(*flagInsecureTLS), optTransportConfig)
		for n := 0; n < flag.NArg(); n++ {
			arg := flag.Arg(n)
			br, ok := blob.Parse(arg)
			if !ok {
				log.Fatalf("Failed to parse argument %q as a blobref.", arg)
			}
			items = append(items, br)
		}
	}

	httpStats := cl.HTTPStats()

	diskCacheFetcher, err := cacher.NewDiskCache(cl)
	if err != nil {
		log.Fatalf("Error setting up local disk cache: %v", err)
	}
	defer diskCacheFetcher.Clean()
	if *flagVerbose {
		log.Printf("Using temp blob cache directory %s", diskCacheFetcher.Root)
	}

	for _, br := range items {
		if *flagGraph {
			printGraph(diskCacheFetcher, br)
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
				rc, err = schema.NewFileReader(diskCacheFetcher, br)
				if err == nil {
					rc.(*schema.FileReader).LoadAllChunks()
				}
			} else {
				rc, err = fetch(diskCacheFetcher, br)
			}
			if err != nil {
				log.Fatal(err)
			}
			defer rc.Close()
			if _, err := io.Copy(os.Stdout, rc); err != nil {
				log.Fatalf("Failed reading %q: %v", br, err)
			}
		} else {
			if err := smartFetch(diskCacheFetcher, *flagOutput, br); err != nil {
				log.Fatal(err)
			}
		}
	}

	if *flagVerbose {
		log.Printf("HTTP requests: %d\n", httpStats.Requests())
		h1, h2 := httpStats.ProtoVersions()
		log.Printf("    responses: %d (h1), %d (h2)\n", h1, h2)
	}
}

func fetch(src blob.Fetcher, br blob.Ref) (r io.ReadCloser, err error) {
	if *flagVerbose {
		log.Printf("Fetching %s", br.String())
	}
	r, _, err = src.Fetch(br)
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch %s: %s", br, err)
	}
	return r, err
}

// A little less than the sniffer will take, so we don't truncate.
const sniffSize = 900 * 1024

// smartFetch the things that blobs point to, not just blobs.
func smartFetch(src blob.Fetcher, targ string, br blob.Ref) error {
	rc, err := fetch(src, br)
	if err != nil {
		return err
	}
	rcc := types.NewOnceCloser(rc)
	defer rcc.Close()

	sniffer := index.NewBlobSniffer(br)
	_, err = io.CopyN(sniffer, rc, sniffSize)
	if err != nil && err != io.EOF {
		return err
	}

	sniffer.Parse()
	b, ok := sniffer.SchemaBlob()

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
	rcc.Close()

	switch b.Type() {
	case "directory":
		dir := filepath.Join(targ, b.FileName())
		if *flagVerbose {
			log.Printf("Fetching directory %v into %s", br, dir)
		}
		if err := os.MkdirAll(dir, b.FileMode()); err != nil {
			return err
		}
		if err := setFileMeta(dir, b); err != nil {
			log.Print(err)
		}
		entries, ok := b.DirectoryEntries()
		if !ok {
			return fmt.Errorf("bad entries blobref in dir %v", b.BlobRef())
		}
		return smartFetch(src, dir, entries)
	case "static-set":
		if *flagVerbose {
			log.Printf("Fetching directory entries %v into %s", br, targ)
		}

		// directory entries
		const numWorkers = 10
		type work struct {
			br   blob.Ref
			errc chan<- error
		}
		members := b.StaticSetMembers()
		workc := make(chan work, len(members))
		defer close(workc)
		for i := 0; i < numWorkers; i++ {
			go func() {
				for wi := range workc {
					wi.errc <- smartFetch(src, targ, wi.br)
				}
			}()
		}
		var errcs []<-chan error
		for _, mref := range members {
			errc := make(chan error, 1)
			errcs = append(errcs, errc)
			workc <- work{mref, errc}
		}
		for _, errc := range errcs {
			if err := <-errc; err != nil {
				return err
			}
		}
		return nil
	case "file":
		fr, err := schema.NewFileReader(src, br)
		if err != nil {
			return fmt.Errorf("NewFileReader: %v", err)
		}
		fr.LoadAllChunks()
		defer fr.Close()

		name := filepath.Join(targ, b.FileName())

		if fi, err := os.Stat(name); err == nil && fi.Size() == fr.Size() {
			if *flagVerbose {
				log.Printf("Skipping %s; already exists.", name)
			}
			return nil
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
		if err := setFileMeta(name, b); err != nil {
			log.Print(err)
		}
		return nil
	case "symlink":
		if *flagSkipIrregular {
			return nil
		}
		sf, ok := b.AsStaticFile()
		if !ok {
			return errors.New("blob is not a static file")
		}
		sl, ok := sf.AsStaticSymlink()
		if !ok {
			return errors.New("blob is not a symlink")
		}
		name := filepath.Join(targ, sl.FileName())
		if _, err := os.Lstat(name); err == nil {
			if *flagVerbose {
				log.Printf("Skipping creating symbolic link %s: A file with that name exists", name)
			}
			return nil
		}
		target := sl.SymlinkTargetString()
		if target == "" {
			return errors.New("symlink without target")
		}

		// On Windows, os.Symlink isn't yet implemented as of Go 1.3.
		// See https://code.google.com/p/go/issues/detail?id=5750
		err := os.Symlink(target, name)
		// We won't call setFileMeta for a symlink because:
		// the permissions of a symlink do not matter and Go's
		// os.Chtimes always dereferences (does not act on the
		// symlink but its target).
		return err
	case "fifo":
		if *flagSkipIrregular {
			return nil
		}
		name := filepath.Join(targ, b.FileName())

		sf, ok := b.AsStaticFile()
		if !ok {
			return errors.New("blob is not a static file")
		}
		_, ok = sf.AsStaticFIFO()
		if !ok {
			return errors.New("blob is not a static FIFO")
		}

		if _, err := os.Lstat(name); err == nil {
			log.Printf("Skipping FIFO %s: A file with that name already exists", name)
			return nil
		}

		err = osutil.Mkfifo(name, 0600)
		if err == osutil.ErrNotSupported {
			log.Printf("Skipping FIFO %s: Unsupported filetype", name)
			return nil
		}
		if err != nil {
			return fmt.Errorf("%s: osutil.Mkfifo(): %v", name, err)
		}

		if err := setFileMeta(name, b); err != nil {
			log.Print(err)
		}

		return nil

	case "socket":
		if *flagSkipIrregular {
			return nil
		}
		name := filepath.Join(targ, b.FileName())

		sf, ok := b.AsStaticFile()
		if !ok {
			return errors.New("blob is not a static file")
		}
		_, ok = sf.AsStaticSocket()
		if !ok {
			return errors.New("blob is not a static socket")
		}

		if _, err := os.Lstat(name); err == nil {
			log.Printf("Skipping socket %s: A file with that name already exists", name)
			return nil
		}

		err = osutil.Mksocket(name)
		if err == osutil.ErrNotSupported {
			log.Printf("Skipping socket %s: Unsupported filetype", name)
			return nil
		}
		if err != nil {
			return fmt.Errorf("%s: %v", name, err)
		}

		if err := setFileMeta(name, b); err != nil {
			log.Print(err)
		}

		return nil

	default:
		return errors.New("unknown blob type: " + b.Type())
	}
	panic("unreachable")
}

func setFileMeta(name string, blob *schema.Blob) error {
	err1 := os.Chmod(name, blob.FileMode())
	var err2 error
	if mt := blob.ModTime(); !mt.IsZero() {
		err2 = os.Chtimes(name, mt, mt)
	}
	// TODO: we previously did os.Chown here, but it's rarely wanted,
	// then the schema.Blob refactor broke it, so it's gone.
	// Add it back later once we care?
	for _, err := range []error{err1, err2} {
		if err != nil {
			return err
		}
	}
	return nil
}
