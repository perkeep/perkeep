/*
Copyright 2026 The Perkeep Authors

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

package cloudstorage

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// fakeGCS is an in-memory implementation of the subset of the GCS
// JSON and XML APIs used by Storage, including the hardcoded XML API
// requests made by go4.org/cloud/google/gcsutil.
type fakeGCS struct {
	bucket string

	// afterStat, if non-nil, is called (without mu held) after
	// an object metadata request has looked up the object but
	// before the response is written.
	afterStat func(name string)

	mu      sync.Mutex
	objects map[string][]byte // object name => contents
	uploads map[string]int    // object name => number of upload attempts
	// statErr, if non-zero, is the HTTP status returned for
	// object metadata requests.
	statErr int
}

func newFakeGCS(bucket string) *fakeGCS {
	return &fakeGCS{
		bucket:  bucket,
		objects: make(map[string][]byte),
		uploads: make(map[string]int),
	}
}

func (f *fakeGCS) put(name string, contents []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[name] = contents
}

func (f *fakeGCS) get(name string) (contents []byte, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	contents, ok = f.objects[name]
	return contents, ok
}

func (f *fakeGCS) numUploads(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.uploads[name]
}

func (f *fakeGCS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.EscapedPath()
	switch {
	case strings.HasPrefix(p, "/upload/storage/v1/b/"+f.bucket+"/o"):
		if r.Method != "POST" {
			break
		}
		f.serveUpload(w, r)
		return
	case strings.HasPrefix(p, "/storage/v1/b/"+f.bucket+"/o/"):
		name, err := url.PathUnescape(strings.TrimPrefix(p, "/storage/v1/b/"+f.bucket+"/o/"))
		if err != nil {
			break
		}
		switch r.Method {
		case "GET":
			if r.FormValue("alt") == "media" {
				f.serveMedia(w, r, name)
			} else {
				f.serveAttrs(w, name)
			}
			return
		case "DELETE":
			f.serveDelete(w, name)
			return
		}
	case p == "/"+f.bucket+"/":
		if r.Method == "GET" {
			f.serveList(w, r)
			return
		}
	case strings.HasPrefix(p, "/"+f.bucket+"/"):
		name, err := url.PathUnescape(strings.TrimPrefix(p, "/"+f.bucket+"/"))
		if err != nil {
			break
		}
		if r.Method == "GET" {
			f.serveMedia(w, r, name)
			return
		}
	}
	jsonError(w, http.StatusNotImplemented, fmt.Sprintf("fakeGCS: unhandled %s %s", r.Method, r.URL))
}

func jsonError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": code, "message": msg},
	})
}

func (f *fakeGCS) writeAttrs(w http.ResponseWriter, name string, size int) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"kind":       "storage#object",
		"bucket":     f.bucket,
		"name":       name,
		"size":       strconv.Itoa(size),
		"generation": "1",
	})
}

func (f *fakeGCS) serveAttrs(w http.ResponseWriter, name string) {
	f.mu.Lock()
	contents, ok := f.objects[name]
	statErr := f.statErr
	f.mu.Unlock()
	if f.afterStat != nil {
		f.afterStat(name)
	}
	if statErr != 0 {
		jsonError(w, statErr, "injected error")
		return
	}
	if !ok {
		jsonError(w, http.StatusNotFound, "No such object")
		return
	}
	f.writeAttrs(w, name, len(contents))
}

func (f *fakeGCS) serveMedia(w http.ResponseWriter, r *http.Request, name string) {
	f.mu.Lock()
	contents, ok := f.objects[name]
	f.mu.Unlock()
	if !ok {
		jsonError(w, http.StatusNotFound, "No such object")
		return
	}
	w.Header().Set("X-Goog-Generation", "1")
	w.Header().Set("X-Goog-Metageneration", "1")
	w.Header().Set("X-Goog-Stored-Content-Length", strconv.Itoa(len(contents)))
	rng := r.Header.Get("Range")
	if rng == "" {
		w.Header().Set("Content-Length", strconv.Itoa(len(contents)))
		w.Write(contents)
		return
	}
	var start, end int
	if n, _ := fmt.Sscanf(rng, "bytes=%d-%d", &start, &end); n == 0 {
		jsonError(w, http.StatusBadRequest, "bad Range")
		return
	} else if n == 1 || end >= len(contents) {
		end = len(contents) - 1
	}
	if start >= len(contents) {
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(contents)))
	w.Header().Set("Content-Length", strconv.Itoa(end-start+1))
	w.WriteHeader(http.StatusPartialContent)
	w.Write(contents[start : end+1])
}

func (f *fakeGCS) serveDelete(w http.ResponseWriter, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.objects[name]; !ok {
		jsonError(w, http.StatusNotFound, "No such object")
		return
	}
	delete(f.objects, name)
	w.WriteHeader(http.StatusNoContent)
}

// serveUpload handles a multipart (non-resumable) object upload.
func (f *fakeGCS) serveUpload(w http.ResponseWriter, r *http.Request) {
	if ut := r.FormValue("uploadType"); ut != "multipart" {
		jsonError(w, http.StatusNotImplemented, "unsupported uploadType "+ut)
		return
	}
	_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	mr := multipart.NewReader(r.Body, params["boundary"])
	var meta struct{ Name string }
	part, err := mr.NextPart()
	if err == nil {
		err = json.NewDecoder(part).Decode(&meta)
	}
	if err != nil {
		jsonError(w, http.StatusBadRequest, "bad metadata part: "+err.Error())
		return
	}
	part, err = mr.NextPart()
	var contents []byte
	if err == nil {
		contents, err = io.ReadAll(part)
	}
	if err != nil {
		jsonError(w, http.StatusBadRequest, "bad media part: "+err.Error())
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploads[meta.Name]++
	if _, ok := f.objects[meta.Name]; ok && r.FormValue("ifGenerationMatch") == "0" {
		jsonError(w, http.StatusPreconditionFailed, "At least one of the pre-conditions you specified did not hold.")
		return
	}
	f.objects[meta.Name] = contents
	f.writeAttrs(w, meta.Name, len(contents))
}

// serveList handles an XML API bucket listing, as done by
// gcsutil.EnumerateObjects.
func (f *fakeGCS) serveList(w http.ResponseWriter, r *http.Request) {
	marker := r.FormValue("marker")
	maxKeys := 1000
	if v := r.FormValue("max-keys"); v != "" {
		var err error
		if maxKeys, err = strconv.Atoi(v); err != nil {
			jsonError(w, http.StatusBadRequest, "bad max-keys")
			return
		}
	}

	type content struct {
		Key  string
		Size int
	}
	var res struct {
		XMLName  xml.Name `xml:"ListBucketResult"`
		Contents []content
	}
	f.mu.Lock()
	for name, contents := range f.objects {
		if name > marker {
			res.Contents = append(res.Contents, content{name, len(contents)})
		}
	}
	f.mu.Unlock()
	slices.SortFunc(res.Contents, func(a, b content) int { return strings.Compare(a.Key, b.Key) })
	if len(res.Contents) > maxKeys {
		res.Contents = res.Contents[:maxKeys]
	}
	w.Header().Set("Content-Type", "application/xml")
	xml.NewEncoder(w).Encode(res)
}
