/*
Copyright 2011 The Perkeep Authors

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
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"go4.org/readerutil"
	"perkeep.org/internal/httputil"
	"perkeep.org/internal/magic"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/cacher"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/search"
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

	ForceMIME   string // optional
	forceInline bool   // to force Content-Disposition to inline, when it was not set in the request

	// pathByRef maps a file Ref to the path of the file, relative to its ancestor
	// directory which was requested for download. It is populated by checkFiles, which
	// only runs if Fetcher is a caching Fetcher.
	pathByRef map[blob.Ref]string

	// r is the incoming http request. it is stored in the DownloadHandler so we
	// don't have to clutter all the func signatures to pass it all the way down to
	// fileInfoPacked.
	r *http.Request
}

type fileInfo struct {
	mime     string
	name     string
	size     int64
	modtime  time.Time
	mode     os.FileMode
	rs       io.ReadSeeker
	close    func() error // release the rs
	whyNot   string       // for testing, why fileInfoPacked failed.
	isDir    bool
	children []blob.Ref // directory entries, if we're a dir.
}

var errNotDir = errors.New("not a directory")

// dirInfo checks whether maybeDir is a directory schema, and if so returns the
// corresponding fileInfo. If dir is another kind of (valid) file schema, errNotDir
// is returned.
func (dh *DownloadHandler) dirInfo(ctx context.Context, dir blob.Ref) (fi fileInfo, err error) {
	rc, _, err := dh.Fetcher.Fetch(ctx, dir)
	if err != nil {
		return fi, fmt.Errorf("could not fetch %v: %v", dir, err)
	}
	b, err := schema.BlobFromReader(dir, rc)
	rc.Close()
	if err != nil {
		return fi, fmt.Errorf("could not read %v as blob: %v", dir, err)
	}
	tp := b.Type()
	if tp != "directory" {
		return fi, errNotDir
	}
	dr, err := schema.NewDirReader(ctx, dh.Fetcher, dir)
	if err != nil {
		return fi, fmt.Errorf("could not open %v as directory: %v", dir, err)
	}
	children, err := dr.StaticSet(ctx)
	if err != nil {
		return fi, fmt.Errorf("could not get dir entries of %v: %v", dir, err)
	}
	return fileInfo{
		isDir:    true,
		name:     b.FileName(),
		modtime:  b.ModTime(),
		children: children,
	}, nil
}

func (dh *DownloadHandler) fileInfo(ctx context.Context, file blob.Ref) (fi fileInfo, packed bool, err error) {
	// Need to get the type first, because we can't use NewFileReader on a non-regular file.
	// TODO(mpl): should we let NewFileReader be ok with non-regular files? and fail later when e.g. trying to read?
	rc, _, err := dh.Fetcher.Fetch(ctx, file)
	if err != nil {
		return fi, false, fmt.Errorf("could not fetch %v: %v", file, err)
	}
	b, err := schema.BlobFromReader(file, rc)
	rc.Close()
	if err != nil {
		return fi, false, fmt.Errorf("could not read %v as blob: %v", file, err)
	}
	tp := b.Type()
	if tp != schema.TypeFile {
		// for non-regular files
		var contents string
		if tp == schema.TypeSymlink {
			sf, _ := b.AsStaticFile()
			sl, _ := sf.AsStaticSymlink()
			contents = sl.SymlinkTargetString()
		}
		size := int64(len(contents))
		// TODO(mpl): make sure that works on windows too
		rd := strings.NewReader(contents)
		fi = fileInfo{
			size:    size,
			modtime: b.ModTime(),
			name:    b.FileName(),
			mode:    b.FileMode(),
			rs:      readerutil.NewFakeSeeker(rd, size),
			close:   io.NopCloser(rd).Close,
		}
		return fi, false, nil
	}

	// Fast path for blobpacked.
	fi, ok := fileInfoPacked(ctx, dh.Search, dh.Fetcher, dh.r, file)
	if debugPack {
		log.Printf("download.go: fileInfoPacked: ok=%v, %+v", ok, fi)
	}
	if ok {
		return fi, true, nil
	}

	fr, err := schema.NewFileReader(ctx, dh.Fetcher, file)
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
		mode:    fr.FileMode(),
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

	var modtime time.Time
	if !fi.ModTime.IsAnyZero() {
		modtime = fi.ModTime.Time()
	} else if !fi.Time.IsAnyZero() {
		modtime = fi.Time.Time()
	}
	// TODO(mpl): it'd be nicer to get the FileMode from the describe response,
	// instead of having to fetch the file schema again, but we don't index the
	// FileMode for now, so it's not just a matter of adding the FileMode to
	// camtypes.FileInfo
	fr, err := schema.NewFileReader(ctx, src, file)
	fr.Close()
	if err != nil {
		return fileInfo{whyNot: fmt.Sprintf("cannot open a file reader: %v", err)}, false
	}
	return fileInfo{
		mime:    fi.MIMEType,
		name:    fi.FileName,
		size:    fi.Size,
		modtime: modtime,
		mode:    fr.FileMode(),
		rs:      readerutil.NewFakeSeeker(rc, fi.Size-offset),
		close:   rc.Close,
	}, true
}

// ServeHTTP answers the following queries:
//
// POST:
//
//	?files=sha1-foo,sha1-bar,sha1-baz
//
// Creates a zip archive of the provided files and serves it in the response.
//
// GET:
//
//	/<file-schema-blobref>[?inline=1]
//
// Serves the file described by the requested file schema blobref.
// if inline=1 the Content-Disposition of the response is set to inline, and
// otherwise it set to attachment.
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
	ctx := r.Context()
	if r.Method != "GET" && r.Method != "HEAD" {
		http.Error(w, "Invalid download method", http.StatusBadRequest)
		return
	}

	if r.Header.Get("If-Modified-Since") != "" {
		// Immutable, so any copy's a good copy.
		w.WriteHeader(http.StatusNotModified)
		return
	}

	dh.r = r
	fi, packed, err := dh.fileInfo(ctx, file)
	if err != nil {
		http.Error(w, "Can't serve file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if !fi.mode.IsRegular() {
		http.Error(w, "Not a regular file", http.StatusBadRequest)
		return
	}
	defer fi.close()

	h := w.Header()
	h.Set("Content-Length", fmt.Sprint(fi.size))
	h.Set("Expires", time.Now().Add(oneYear).Format(http.TimeFormat))
	if packed {
		h.Set("X-Camlistore-Packed", "1")
	}

	fileName := func(ext string) string {
		if fi.name != "" {
			return fi.name
		}
		return "file-" + file.String() + ext
	}

	if r.FormValue("inline") == "1" || dh.forceInline {
		// TODO(mpl): investigate why at least text files have an incorrect MIME.
		if fi.mime == "application/octet-stream" {
			// Since e.g. plain text files are seen as "application/octet-stream", we force
			// check for that, so we can have the browser display them as text if they are
			// indeed actually text.
			text, err := isText(fi.rs)
			if err != nil {
				// TODO: https://perkeep.org/issues/1060
				httputil.ServeError(w, r, fmt.Errorf("cannot verify MIME type of file: %v", err))
				return
			}
			if text {
				fi.mime = "text/plain"
			}
		}
		h.Set("Content-Disposition", "inline")
	} else {
		w.Header().Set("Content-Disposition", "attachment; filename="+fileName(".dat"))
	}
	h.Set("Content-Type", fi.mime)

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

// isText reports whether the first MB read from rs is valid UTF-8 text.
func isText(rs io.ReadSeeker) (ok bool, err error) {
	defer func() {
		if _, seekErr := rs.Seek(0, io.SeekStart); seekErr != nil {
			if err == nil {
				err = seekErr
			}
		}
	}()
	var buf bytes.Buffer
	if _, err := io.CopyN(&buf, rs, 1e6); err != nil {
		if err != io.EOF {
			return false, err
		}
	}
	return utf8.Valid(buf.Bytes()), nil
}

// statFiles stats the given refs and returns an error if any one of them is not
// found.
// It is the responsibility of the caller to check that dh.Fetcher is a
// blobserver.BlobStatter.
func (dh *DownloadHandler) statFiles(refs []blob.Ref) error {
	statter, ok := dh.Fetcher.(blobserver.BlobStatter)
	if !ok {
		return fmt.Errorf("DownloadHandler.Fetcher %T is not a BlobStatter", dh.Fetcher)
	}
	statted := make(map[blob.Ref]bool)

	err := statter.StatBlobs(context.TODO(), refs, func(sb blob.SizedRef) error {
		statted[sb.Ref] = true
		return nil
	})
	if err != nil {
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

var allowedFileTypes = map[schema.CamliType]bool{
	schema.TypeFile:    true,
	schema.TypeSymlink: true,
	schema.TypeFIFO:    true,
	schema.TypeSocket:  true,
}

// checkFiles reads, and discards, the file contents for each of the given file refs.
// It is used to check that all files requested for download are readable before
// starting to reply and/or creating a zip archive of them. It recursively
// checks directories as well. It also populates dh.pathByRef.
func (dh *DownloadHandler) checkFiles(ctx context.Context, parentPath string, fileRefs []blob.Ref) error {
	// TODO(mpl): add some concurrency
	for _, br := range fileRefs {
		rc, _, err := dh.Fetcher.Fetch(ctx, br)
		if err != nil {
			return fmt.Errorf("could not fetch %v: %v", br, err)
		}
		b, err := schema.BlobFromReader(br, rc)
		rc.Close()
		if err != nil {
			return fmt.Errorf("could not read %v as blob: %v", br, err)
		}
		tp := b.Type()
		if _, ok := allowedFileTypes[tp]; !ok && tp != schema.TypeDirectory {
			return fmt.Errorf("%v not a supported file or directory type: %q", br, tp)
		}
		if tp == schema.TypeDirectory {
			dr, err := b.NewDirReader(ctx, dh.Fetcher)
			if err != nil {
				return fmt.Errorf("could not open %v as directory: %v", br, err)
			}
			children, err := dr.StaticSet(ctx)
			if err != nil {
				return fmt.Errorf("could not get dir entries of %v: %v", br, err)
			}
			if err := dh.checkFiles(ctx, filepath.Join(parentPath, b.FileName()), children); err != nil {
				return err
			}
			continue
		}
		if tp != schema.TypeFile {
			// We only bother checking regular files. symlinks, fifos, and sockets are
			// assumed ok.
			dh.pathByRef[br] = filepath.Join(parentPath, b.FileName())
			continue
		}
		fr, err := b.NewFileReader(dh.Fetcher)
		if err != nil {
			return fmt.Errorf("could not open %v: %v", br, err)
		}
		_, err = io.Copy(io.Discard, fr)
		fr.Close()
		if err != nil {
			return fmt.Errorf("could not read %v: %v", br, err)
		}
		dh.pathByRef[br] = filepath.Join(parentPath, b.FileName())
	}
	return nil
}

// serveZip creates a zip archive from the files provided as
// ?files=sha1-foo,sha1-bar,... and serves it as the response.
func (dh *DownloadHandler) serveZip(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != "POST" {
		http.Error(w, "Invalid download method", http.StatusBadRequest)
		return
	}

	filesValue := r.FormValue("files")
	if filesValue == "" {
		http.Error(w, "No file blobRefs specified", http.StatusBadRequest)
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
	var allRefs map[blob.Ref]string
	_, ok := (dh.Fetcher).(*cacher.CachingFetcher)
	if ok {
		// If we have a caching fetcher, allRefs and dh.pathByRef are populated with all
		// the input refs plus their children, so we don't have to redo later the recursing
		// work that we're alreading doing in checkFiles.
		dh.pathByRef = make(map[blob.Ref]string, len(refs))
		err := dh.checkFiles(ctx, "", refs)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		allRefs = dh.pathByRef
	} else {
		_, ok := dh.Fetcher.(blobserver.BlobStatter)
		if ok {
			if err := dh.statFiles(refs); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		// If we don't have a cacher we don't know yet of all the possible
		// children refs, so allRefs is just the input refs, and the
		// children will be discovered on the fly, while building the zip archive.
		// This is the case even if we have a statter, because statFiles does not
		// recurse into directories.
		allRefs = make(map[blob.Ref]string, len(refs))
		for _, v := range refs {
			allRefs[v] = ""
		}
	}

	h := w.Header()
	h.Set("Content-Type", "application/zip")
	zipName := "camli-download-" + time.Now().Format(downloadTimeLayout) + ".zip"
	h.Set("Content-Disposition", "attachment; filename="+zipName)
	zw := zip.NewWriter(w)
	dh.r = r
	for br := range allRefs {
		if err := dh.zipFile(ctx, "", br, zw); err != nil {
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

// zipFile, if br is a file, adds br to the zip archive that zw writes to. If br
// is a directory, zipFile adds all its files descendants to the zip. parentPath is
// the path to the parent directory of br. It is only used if dh.pathByRef has not
// been populated (i.e. if dh does not use a caching fetcher).
func (dh *DownloadHandler) zipFile(ctx context.Context, parentPath string, br blob.Ref, zw *zip.Writer) error {
	if len(dh.pathByRef) == 0 {
		// if dh.pathByRef is not populated, we have to check for ourselves now whether
		// br is a directory.
		di, err := dh.dirInfo(ctx, br)
		if err != nil && err != errNotDir {
			return err
		}
		if di.isDir {
			for _, v := range di.children {
				if err := dh.zipFile(ctx, filepath.Join(parentPath, di.name), v, zw); err != nil {
					return err
				}
			}
			return nil
		}
	}
	fi, _, err := dh.fileInfo(ctx, br)
	if err != nil {
		return err
	}
	defer fi.close()
	filename, ok := dh.pathByRef[br]
	if !ok {
		// because we're in the len(dh.pathByRef) == 0 case.
		filename = filepath.Join(parentPath, fi.name)
	}
	zh := &zip.FileHeader{
		Name:     filename,
		Method:   zip.Store,
		Modified: fi.modtime.UTC(),
	}
	zh.SetMode(fi.mode)
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
