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

package server

import (
	"archive/zip"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/cacher"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/magic"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/search"
	"go4.org/readerutil"
	"golang.org/x/net/context"
)

const (
	oneYear            = 365 * 86400 * time.Second
	downloadTimeLayout = "20060102150405"
)

var (
	debugPack = strings.Contains(os.Getenv("CAMLI_DEBUG_X"), "packserve")

	// Download URL suffix:
	//  $1: blobref (checked in download handler)
	//  $2: TODO. optional "/filename" to be sent as recommended download name,
	//    if sane looking
	downloadPattern = regexp.MustCompile(`^download/([^/]+)(/.*)?$`)
)

type DownloadHandler struct {
	Fetcher blob.Fetcher

	// Search is optional. If present, it's used to map a fileref
	// to a wholeref, if the Fetcher is of a type that knows how
	// to get at a wholeref more efficiently. (e.g. blobpacked)
	Search *search.Handler

	ForceMIME string // optional
}

type fileInfo struct {
	mime    string
	name    string // base name of the file
	size    int64
	modtime time.Time
	rs      io.ReadSeeker
	close   func() error // release the rs
	whyNot  string       // for testing, why fileInfoPacked failed.
}

func (dh *DownloadHandler) fileInfo(r *http.Request, file blob.Ref) (fi fileInfo, packed bool, err error) {
	ctx := context.TODO()

	// Fast path for blobpacked.
	fi, ok := fileInfoPacked(ctx, dh.Search, dh.Fetcher, r, file)
	if debugPack {
		log.Printf("download.go: fileInfoPacked: ok=%v, %+v", ok, fi)
	}
	if ok {
		return fi, true, nil
	}
	fr, err := schema.NewFileReader(dh.Fetcher, file)
	if err != nil {
		return
	}
	mime := dh.ForceMIME
	if mime == "" {
		mime = magic.MIMETypeFromReaderAt(fr)
	}
	if mime == "" {
		mime = "application/octet-stream"
	}
	return fileInfo{
		mime:    mime,
		name:    fr.FileName(),
		size:    fr.Size(),
		modtime: fr.ModTime(),
		rs:      fr,
		close:   fr.Close,
	}, false, nil
}

// Fast path for blobpacked.
func fileInfoPacked(ctx context.Context, sh *search.Handler, src blob.Fetcher, r *http.Request, file blob.Ref) (packFileInfo fileInfo, ok bool) {
	if sh == nil {
		return fileInfo{whyNot: "no search"}, false
	}
	wf, ok := src.(blobserver.WholeRefFetcher)
	if !ok {
		return fileInfo{whyNot: "fetcher type"}, false
	}
	if r != nil && r.Header.Get("Range") != "" {
		// TODO: not handled yet. Maybe not even important,
		// considering rarity.
		return fileInfo{whyNot: "range header"}, false
	}
	des, err := sh.Describe(ctx, &search.DescribeRequest{BlobRef: file})
	if err != nil {
		log.Printf("ui: fileInfoPacked: skipping fast path due to error from search: %v", err)
		return fileInfo{whyNot: "search error"}, false
	}
	db, ok := des.Meta[file.String()]
	if !ok || db.File == nil {
		return fileInfo{whyNot: "search index doesn't know file"}, false
	}
	fi := db.File
	if !fi.WholeRef.Valid() {
		return fileInfo{whyNot: "no wholeref from search index"}, false
	}

	offset := int64(0)
	rc, wholeSize, err := wf.OpenWholeRef(fi.WholeRef, offset)
	if err == os.ErrNotExist {
		return fileInfo{whyNot: "WholeRefFetcher returned ErrNotexist"}, false
	}
	if wholeSize != fi.Size {
		log.Printf("ui: fileInfoPacked: OpenWholeRef size %d != index size %d; ignoring fast path", wholeSize, fi.Size)
		return fileInfo{whyNot: "WholeRefFetcher and index don't agree"}, false
	}
	if err != nil {
		log.Printf("ui: fileInfoPacked: skipping fast path due to error from WholeRefFetcher (%T): %v", src, err)
		return fileInfo{whyNot: "WholeRefFetcher error"}, false
	}
	modtime := fi.ModTime
	if modtime.IsAnyZero() {
		modtime = fi.Time
	}
	return fileInfo{
		mime:    fi.MIMEType,
		name:    fi.FileName,
		size:    fi.Size,
		modtime: modtime.Time(),
		rs:      readerutil.NewFakeSeeker(rc, fi.Size-offset),
		close:   rc.Close,
	}, true
}

// ServeHTTP answers the following queries:
//
// POST:
//   ?files=sha1-foo,sha1-bar,sha1-baz
// Creates a zip archive of the provided files and serves it in the response.
//
// GET:
//   /<file-schema-blobref>
// Serves the file described by the requested file schema blobref.
func (dh *DownloadHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		dh.serveZip(w, r)
		return
	}

	suffix := httputil.PathSuffix(r)
	m := downloadPattern.FindStringSubmatch(suffix)
	if m == nil {
		httputil.ErrorRouting(w, r)
		return
	}
	file, ok := blob.Parse(m[1])
	if !ok {
		http.Error(w, "Invalid blobref", http.StatusBadRequest)
		return
	}
	// TODO(mpl): make use of m[2] (the optional filename).
	dh.ServeFile(w, r, file)
}

func (dh *DownloadHandler) ServeFile(w http.ResponseWriter, r *http.Request, file blob.Ref) {
	if r.Method != "GET" && r.Method != "HEAD" {
		http.Error(w, "Invalid download method", http.StatusBadRequest)
		return
	}

	if r.Header.Get("If-Modified-Since") != "" {
		// Immutable, so any copy's a good copy.
		w.WriteHeader(http.StatusNotModified)
		return
	}

	fi, packed, err := dh.fileInfo(r, file)
	if err != nil {
		http.Error(w, "Can't serve file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer fi.close()

	h := w.Header()
	h.Set("Content-Length", fmt.Sprint(fi.size))
	h.Set("Expires", time.Now().Add(oneYear).Format(http.TimeFormat))
	h.Set("Content-Type", fi.mime)
	if packed {
		h.Set("X-Camlistore-Packed", "1")
	}

	if fi.mime == "application/octet-stream" {
		// Chrome seems to silently do nothing on
		// application/octet-stream unless this is set.
		// Maybe it's confused by lack of URL it recognizes
		// along with lack of mime type?
		fileName := fi.name
		if fileName == "" {
			fileName = "file-" + file.String() + ".dat"
		}
		w.Header().Set("Content-Disposition", "attachment; filename="+fileName)
	}

	if r.Method == "HEAD" && r.FormValue("verifycontents") != "" {
		vbr, ok := blob.Parse(r.FormValue("verifycontents"))
		if !ok {
			return
		}
		hash := vbr.Hash()
		if hash == nil {
			return
		}
		io.Copy(hash, fi.rs) // ignore errors, caught later
		if vbr.HashMatches(hash) {
			w.Header().Set("X-Camli-Contents", vbr.String())
		}
		return
	}

	http.ServeContent(w, r, "", time.Now(), fi.rs)
}

// statFiles stats the given refs and returns an error if any one of them is not
// found.
// It is the responsibility of the caller to check that dh.Fetcher is a
// blobserver.BlobStatter.
func (dh *DownloadHandler) statFiles(refs []blob.Ref) error {
	statter, _ := dh.Fetcher.(blobserver.BlobStatter)
	statted := make(map[blob.Ref]bool)
	ch := make(chan (blob.SizedRef))
	errc := make(chan (error))
	go func() {
		err := statter.StatBlobs(ch, refs)
		close(ch)
		errc <- err

	}()
	for sbr := range ch {
		statted[sbr.Ref] = true
	}
	if err := <-errc; err != nil {
		log.Printf("Error statting blob files for download archive: %v", err)
		return fmt.Errorf("error looking for files")
	}
	for _, v := range refs {
		if _, ok := statted[v]; !ok {
			return fmt.Errorf("%q was not found", v)
		}
	}
	return nil
}

// checkFiles reads, and discards, the file contents for each of the given file refs.
// It is used to check that all files requested for download are readable before
// starting to reply and/or creating a zip archive of them.
func (dh *DownloadHandler) checkFiles(fileRefs []blob.Ref) error {
	// TODO(mpl): add some concurrency
	for _, br := range fileRefs {
		fr, err := schema.NewFileReader(dh.Fetcher, br)
		if err != nil {
			return fmt.Errorf("could not fetch %v: %v", br, err)
		}
		_, err = io.Copy(ioutil.Discard, fr)
		fr.Close()
		if err != nil {
			return fmt.Errorf("could not read %v: %v", br, err)
		}
	}
	return nil
}

// serveZip creates a zip archive from the files provided as
// ?files=sha1-foo,sha1-bar,... and serves it as the response.
func (dh *DownloadHandler) serveZip(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Invalid download method", http.StatusBadRequest)
		return
	}

	filesValue := r.FormValue("files")
	if filesValue == "" {
		http.Error(w, "No files blobRefs specified", http.StatusBadRequest)
		return
	}
	files := strings.Split(filesValue, ",")

	var refs []blob.Ref
	for _, file := range files {
		br, ok := blob.Parse(file)
		if !ok {
			http.Error(w, fmt.Sprintf("%q is not a valid blobRef", file), http.StatusBadRequest)
			return
		}
		refs = append(refs, br)
	}

	// We check as many things as we can before writing the zip, because
	// once we start sending a response we can't http.Error anymore.
	_, ok := (dh.Fetcher).(*cacher.CachingFetcher)
	if ok {
		if err := dh.checkFiles(refs); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		_, ok := dh.Fetcher.(blobserver.BlobStatter)
		if ok {
			if err := dh.statFiles(refs); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}

	h := w.Header()
	h.Set("Content-Type", "application/zip")
	zipName := "camli-download-" + time.Now().Format(downloadTimeLayout) + ".zip"
	h.Set("Content-Disposition", "attachment; filename="+zipName)
	zw := zip.NewWriter(w)

	zipFile := func(br blob.Ref) error {
		fi, _, err := dh.fileInfo(r, br)
		if err != nil {
			return err
		}
		defer fi.close()
		zh := &zip.FileHeader{
			Name:   fi.name,
			Method: zip.Store,
		}
		zh.SetModTime(fi.modtime)
		zfh, err := zw.CreateHeader(zh)
		if err != nil {
			return err
		}
		_, err = io.Copy(zfh, fi.rs)
		if err != nil {
			return err
		}
		return nil
	}
	for _, br := range refs {
		if err := zipFile(br); err != nil {
			log.Printf("error zipping %v: %v", br, err)
			// http.Error is of no use since we've already started sending a response
			panic(http.ErrAbortHandler)
		}
	}
	if err := zw.Close(); err != nil {
		log.Printf("error closing zip stream: %v", err)
		panic(http.ErrAbortHandler)
	}
}
