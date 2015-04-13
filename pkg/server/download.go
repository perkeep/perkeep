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
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/magic"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/search"
	"camlistore.org/pkg/types"
)

const oneYear = 365 * 86400 * time.Second

var debugPack = strings.Contains(os.Getenv("CAMLI_DEBUG_X"), "packserve")

type DownloadHandler struct {
	Fetcher blob.Fetcher
	Cache   blobserver.Storage

	// Search is optional. If present, it's used to map a fileref
	// to a wholeref, if the Fetcher is of a type that knows how
	// to get at a wholeref more efficiently. (e.g. blobpacked)
	Search *search.Handler

	ForceMIME string // optional
}

func (dh *DownloadHandler) blobSource() blob.Fetcher {
	return dh.Fetcher // TODO: use dh.Cache
}

type fileInfo struct {
	mime   string
	name   string
	size   int64
	rs     io.ReadSeeker
	close  func() error // release the rs
	whyNot string       // for testing, why fileInfoPacked failed.
}

func (dh *DownloadHandler) fileInfo(req *http.Request, file blob.Ref) (fi fileInfo, packed bool, err error) {
	// Fast path for blobpacked.
	fi, ok := fileInfoPacked(dh.Search, dh.Fetcher, req, file)
	if debugPack {
		log.Printf("download.go: fileInfoPacked: ok=%v, %+v", ok, fi)
	}
	if ok {
		return fi, true, nil
	}
	fr, err := schema.NewFileReader(dh.blobSource(), file)
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
		mime:  mime,
		name:  fr.FileName(),
		size:  fr.Size(),
		rs:    fr,
		close: fr.Close,
	}, false, nil
}

// Fast path for blobpacked.
func fileInfoPacked(sh *search.Handler, src blob.Fetcher, req *http.Request, file blob.Ref) (packFileInfo fileInfo, ok bool) {
	if sh == nil {
		return fileInfo{whyNot: "no search"}, false
	}
	wf, ok := src.(blobserver.WholeRefFetcher)
	if !ok {
		return fileInfo{whyNot: "fetcher type"}, false
	}
	if req != nil && req.Header.Get("Range") != "" {
		// TODO: not handled yet. Maybe not even important,
		// considering rarity.
		return fileInfo{whyNot: "range header"}, false
	}
	des, err := sh.Describe(&search.DescribeRequest{BlobRef: file})
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
	return fileInfo{
		mime:  fi.MIMEType,
		name:  fi.FileName,
		size:  fi.Size,
		rs:    types.NewFakeSeeker(rc, fi.Size-offset),
		close: rc.Close,
	}, true
}

func (dh *DownloadHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request, file blob.Ref) {
	if req.Method != "GET" && req.Method != "HEAD" {
		http.Error(rw, "Invalid download method", http.StatusBadRequest)
		return
	}
	if req.Header.Get("If-Modified-Since") != "" {
		// Immutable, so any copy's a good copy.
		rw.WriteHeader(http.StatusNotModified)
		return
	}

	fi, packed, err := dh.fileInfo(req, file)
	if err != nil {
		http.Error(rw, "Can't serve file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer fi.close()

	h := rw.Header()
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
		rw.Header().Set("Content-Disposition", "attachment; filename="+fileName)
	}

	if req.Method == "HEAD" && req.FormValue("verifycontents") != "" {
		vbr, ok := blob.Parse(req.FormValue("verifycontents"))
		if !ok {
			return
		}
		hash := vbr.Hash()
		if hash == nil {
			return
		}
		io.Copy(hash, fi.rs) // ignore errors, caught later
		if vbr.HashMatches(hash) {
			rw.Header().Set("X-Camli-Contents", vbr.String())
		}
		return
	}

	http.ServeContent(rw, req, "", time.Now(), fi.rs)
}
