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
	"fmt"
	"http"
	"io"
	"image"
	"image/jpeg"
	"image/png"
	"json"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"

	"camli/blobref"
	"camli/blobserver"
	"camli/httputil"
	"camli/jsonconfig"
	"camli/misc/resize"
	"camli/misc/vfs" // TODO: ditch this once pkg http gets it
	"camli/schema"
	uistatic "camlistore.org/server/uistatic"
)

var _ = log.Printf

var staticFilePattern = regexp.MustCompile(`^([a-zA-Z0-9\-\_]+\.(html|js|css|png|jpg|gif))$`)
var identPattern = regexp.MustCompile(`^[a-zA-Z\_]+$`)

var uiFiles = uistatic.Files

// Download URL suffix:
//   $1: blobref (checked in download handler)
//   $2: optional "/filename" to be sent as recommended download name,
//       if sane looking
var downloadPattern = regexp.MustCompile(`^download/([^/]+)(/.*)?$`)
var thumbnailPattern = regexp.MustCompile(`^thumbnail/([^/]+)(/.*)?$`)

// UIHandler handles serving the UI and discovery JSON.
type UIHandler struct {
	// URL prefixes (path or full URL) to the primary blob and
	// search root.  Only used by the UI and thus necessary if UI
	// is true.
	BlobRoot     string
	SearchRoot   string
	JSONSignRoot string

	PublishRoots map[string]*PublishHandler

	Storage blobserver.Storage // of BlobRoot
	Cache   blobserver.Storage // or nil
}

func init() {
	blobserver.RegisterHandlerConstructor("ui", newUiFromConfig)
}

func newUiFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (h http.Handler, err os.Error) {
	ui := &UIHandler{}
	ui.BlobRoot = conf.OptionalString("blobRoot", "")
	ui.SearchRoot = conf.OptionalString("searchRoot", "")
	ui.JSONSignRoot = conf.OptionalString("jsonSignRoot", "")
	pubRoots := conf.OptionalList("publishRoots")

	cachePrefix := conf.OptionalString("cache", "")
	if err = conf.Validate(); err != nil {
		return
	}

	ui.PublishRoots = make(map[string]*PublishHandler)
	for _, pubRoot := range pubRoots {
		h, err := ld.GetHandler(pubRoot)
		if err != nil {
			return nil, fmt.Errorf("UI handler's publishRoots references invalid %q", pubRoot)
		}
		pubh, ok := h.(*PublishHandler)
		if !ok {
			return
		}
		ui.PublishRoots[pubh.RootName] = pubh
	}

	checkType := func(key string, htype string) {
		v := conf.OptionalString(key, "")
		if v == "" {
			return
		}
		ct := ld.GetHandlerType(v)
		if ct == "" {
			err = fmt.Errorf("UI handler's %q references non-existant %q", key, v)
		} else if ct != htype {
			err = fmt.Errorf("UI handler's %q references %q of type %q; expected type %q", key, v,
				ct, htype)
		}
	}
	checkType("searchRoot", "search")
	checkType("jsonSignRoot", "jsonsign")
	if err != nil {
		return
	}

	if ui.BlobRoot != "" {
		bs, err := ld.GetStorage(ui.BlobRoot)
		if err != nil {
			return nil, fmt.Errorf("UI handler's blobRoot of %q error: %v", ui.BlobRoot, err)
		}
		ui.Storage = bs
	}

	if cachePrefix != "" {
		bs, err := ld.GetStorage(cachePrefix)
		if err != nil {
			return nil, fmt.Errorf("UI handler's cache of %q error: %v", cachePrefix, err)
		}
		ui.Cache = bs
	}

	return ui, nil
}

func camliMode(req *http.Request) string {
	// TODO-GO: this is too hard to get at the GET Query args on a
	// POST request.
	m, err := http.ParseQuery(req.URL.RawQuery)
	if err != nil {
		return ""
	}
	if mode, ok := m["camli.mode"]; ok && len(mode) > 0 {
		return mode[0]
	}
	return ""
}

func wantsDiscovery(req *http.Request) bool {
	return req.Method == "GET" &&
		(req.Header.Get("Accept") == "text/x-camli-configuration" ||
			camliMode(req) == "config")
}

func wantsUploadHelper(req *http.Request) bool {
	return req.Method == "POST" && camliMode(req) == "uploadhelper"
}

func wantsPermanode(req *http.Request) bool {
	return req.Method == "GET" && blobref.Parse(req.FormValue("p")) != nil
}

func wantsBlobInfo(req *http.Request) bool {
	return req.Method == "GET" && blobref.Parse(req.FormValue("b")) != nil
}

func (ui *UIHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	base := req.Header.Get("X-PrefixHandler-PathBase")
	suffix := req.Header.Get("X-PrefixHandler-PathSuffix")

	rw.Header().Set("Vary", "Accept")
	switch {
	case wantsDiscovery(req):
		ui.serveDiscovery(rw, req)
	case wantsUploadHelper(req):
		ui.serveUploadHelper(rw, req)
	case strings.HasPrefix(suffix, "download/"):
		ui.serveDownload(rw, req)
	case strings.HasPrefix(suffix, "thumbnail/"):
		ui.serveThumbnail(rw, req)
	default:
		file := ""
		if m := staticFilePattern.FindStringSubmatch(suffix); m != nil {
			file = m[1]
		} else {
			switch {
			case wantsPermanode(req):
				file = "permanode.html"
			case wantsBlobInfo(req):
				file = "blobinfo.html"
			case req.URL.Path == base:
				file = "index.html"
			default:
				http.Error(rw, "Illegal URL.", 404)
				return
			}
		}
		vfs.ServeFileFromFS(rw, req, uiFiles, file)
	}
}

func (ui *UIHandler) serveDiscovery(rw http.ResponseWriter, req *http.Request) {
	rw.Header().Set("Content-Type", "text/javascript")
	inCb := false
	if cb := req.FormValue("cb"); identPattern.MatchString(cb) {
		fmt.Fprintf(rw, "%s(", cb)
		inCb = true
	}
	bytes, _ := json.Marshal(map[string]interface{}{
		"blobRoot":       ui.BlobRoot,
		"searchRoot":     ui.SearchRoot,
		"jsonSignRoot":   ui.JSONSignRoot,
		"uploadHelper":   "?camli.mode=uploadhelper", // hack; remove with better javascript
		"downloadHelper": "./download/",
	})
	rw.Write(bytes)
	if inCb {
		rw.Write([]byte{')'})
	}
}

func (ui *UIHandler) storageSeekFetcher() (blobref.SeekFetcher, os.Error) {
	return blobref.SeekerFromStreamingFetcher(ui.Storage)
}

func (ui *UIHandler) serveDownload(rw http.ResponseWriter, req *http.Request) {
	if req.Method != "GET" && req.Method != "HEAD" {
		http.Error(rw, "Invalid download method", 400)
		return
	}
	if ui.Storage == nil {
		http.Error(rw, "No BlobRoot configured", 500)
		return
	}

	fetchSeeker, err := ui.storageSeekFetcher()
	if err != nil {
		http.Error(rw, err.String(), 500)
		return
	}

	suffix := req.Header.Get("X-PrefixHandler-PathSuffix")

	m := downloadPattern.FindStringSubmatch(suffix)
	if m == nil {
		httputil.ErrorRouting(rw, req)
		return
	}

	fbr := blobref.Parse(m[1])
	if fbr == nil {
		http.Error(rw, "Invalid blobref", 400)
		return
	}

	filename := m[2]
	if len(filename) > 0 {
		filename = filename[1:] // remove leading slash
	}

	fr, err := schema.NewFileReader(fetchSeeker, fbr)
	if err != nil {
		http.Error(rw, "Can't serve file: "+err.String(), 500)
		return
	}
	defer fr.Close()

	// TODO: fr.FileSchema() and guess a mime type?  For now:
	schema := fr.FileSchema()
	rw.Header().Set("Content-Type", "application/octet-stream")
	rw.Header().Set("Content-Length", fmt.Sprintf("%d", schema.Size))

	if req.Method == "HEAD" {
		vbr := blobref.Parse(req.FormValue("verifycontents"))
		if vbr == nil {
			return
		}
		hash := vbr.Hash()
		if hash == nil {
			return
		}
		io.Copy(hash, fr) // ignore errors, caught later
		if vbr.HashMatches(hash) {
			rw.Header().Set("X-Camli-Contents", vbr.String())
		}
		return
	}

	n, err := io.Copy(rw, fr)
	log.Printf("For %q request of %s: copied %d, %v", req.Method, req.URL.RawPath, n, err)
	if err != nil {
		log.Printf("error serving download of file schema %s: %v", fbr, err)
		return
	}
	if n != int64(schema.Size) {
		log.Printf("error serving download of file schema %s: sent %d, expected size of %d",
			fbr, n, schema.Size)
		return
	}
}

func (ui *UIHandler) serveThumbnail(rw http.ResponseWriter, req *http.Request) {
	if ui.Storage == nil {
		http.Error(rw, "No BlobRoot configured", 500)
		return
	}

	fetchSeeker, err := ui.storageSeekFetcher()
	if err != nil {
		http.Error(rw, err.String(), 500)
		return
	}

	suffix := req.Header.Get("X-PrefixHandler-PathSuffix")
	m := thumbnailPattern.FindStringSubmatch(suffix)
	if m == nil {
		httputil.ErrorRouting(rw, req)
		return
	}

	query := req.URL.Query()
	width, err := strconv.Atoi(query.Get("mw"))
	if err != nil {
		http.Error(rw, "Invalid specified max width 'mw': "+err.String(), 500)
		return
	}
	height, err := strconv.Atoi(query.Get("mh"))
	if err != nil {
		http.Error(rw, "Invalid specified height 'mh': "+err.String(), 500)
		return
	}

	blobref := blobref.Parse(m[1])
	if blobref == nil {
		http.Error(rw, "Invalid blobref", 400)
		return
	}

	filename := m[2]
	if len(filename) > 0 {
		filename = filename[1:] // remove leading slash
	}

	fr, err := schema.NewFileReader(fetchSeeker, blobref)
	if err != nil {
		http.Error(rw, "Can't serve file: "+err.String(), 500)
		return
	}

	var buf bytes.Buffer
	n, err := io.Copy(&buf, fr)
	i, format, err := image.Decode(&buf)
	if err != nil {
		http.Error(rw, "Can't serve file: "+err.String(), 500)
		return
	}
	b := i.Bounds()
	// only do downscaling, otherwise just serve the original image
	if width < b.Dx() || height < b.Dy() {
		const huge = 2400
		// If it's gigantic, it's more efficient to downsample first
		// and then resize; resizing will smooth out the roughness.
		// (trusting the moustachio guys on that one).
		if b.Dx() > huge || b.Dy() > huge {
			w, h := width*2, height*2
			if b.Dx() > b.Dy() {
				w = b.Dx() * h / b.Dy()
			} else {
				h = b.Dy() * w / b.Dx()
			}
			i = resize.Resample(i, i.Bounds(), w, h)
			b = i.Bounds()
		}
		// conserve proportions. use the smallest of the two as the decisive one.
		if width > height {
			width = b.Dx() * height / b.Dy()
		} else {
			height = b.Dy() * width / b.Dx()
		}
		i = resize.Resize(i, b, width, height)
		// Encode as a new image
		buf.Reset()
		switch format {
		case "jpeg":
			err = jpeg.Encode(&buf, i, nil)
		default:
			err = png.Encode(&buf, i)
		}
		if err != nil {
			http.Error(rw, "Can't serve file: "+err.String(), 500)
			return
		}
	}
	ct := ""
	switch format {
	case "jpeg":
		ct = "image/jpeg"
	default:
		ct = "image/png"
	}
	rw.Header().Set("Content-Type", ct)
	size := buf.Len()
	rw.Header().Set("Content-Length", fmt.Sprintf("%d", size))
	n, err = io.Copy(rw, &buf)
	if err != nil {
		log.Printf("error serving thumbnail of file schema %s: %v", blobref, err)
		return
	}
	if n != int64(size) {
		log.Printf("error serving thumbnail of file schema %s: sent %d, expected size of %d",
			blobref, n, size)
		return
	}
}
