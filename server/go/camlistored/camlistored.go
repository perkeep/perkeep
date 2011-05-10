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
	"flag"
	"fmt"
	"http"
	"json"
	"log"
	"path/filepath"
	"strings"
	"os"

	"camli/auth"
	"camli/blobref"
	"camli/blobserver"
	"camli/blobserver/handlers"
	"camli/errorutil"
	"camli/httputil"
	"camli/jsonconfig"
	"camli/osutil"
	"camli/search"
	"camli/webserver"

	// Storage options:
	_ "camli/blobserver/localdisk"
	_ "camli/blobserver/s3"
	_ "camli/mysqlindexer" // indexer, but uses storage interface
)

var flagConfigFile = flag.String("configfile", "serverconfig",
	"Config file to use, relative to camli config dir root, or blank to not use config files.")

const camliPrefix = "/camli/"

var ErrCamliPath = os.NewError("Invalid Camlistore request path")

func parseCamliPath(path string) (action string, err os.Error) {
	camIdx := strings.Index(path, camliPrefix)
	if camIdx == -1 {
		return "", ErrCamliPath
	}
	action = path[camIdx+len(camliPrefix):]
	return
}

func unsupportedHandler(conn http.ResponseWriter, req *http.Request) {
	httputil.BadRequestError(conn, "Unsupported camlistore path or method.")
}

type storageAndConfig struct {
	blobserver.Storage
	config *blobserver.Config
}

func (s *storageAndConfig) Config() *blobserver.Config {
	return s.config
}

// where prefix is like "/" or "/s3/" for e.g. "/camli/" or "/s3/camli/*"
func makeCamliHandler(prefix, baseURL string, storage blobserver.Storage) http.Handler {
	if !strings.HasSuffix(prefix, "/") {
		panic("expected prefix to end in slash")
	}
	baseURL = strings.TrimRight(baseURL, "/")

	storageConfig := &storageAndConfig{
		storage,
		&blobserver.Config{
			Writable: true,
			Readable: true,
			IsQueue:  false,
			URLBase:  baseURL + prefix[:len(prefix)-1],
		},
	}
	return http.HandlerFunc(func(conn http.ResponseWriter, req *http.Request) {
		action, err := parseCamliPath(req.URL.Path[len(prefix)-1:])
		if err != nil {
			log.Printf("Invalid request for method %q, path %q",
				req.Method, req.URL.Path)
			unsupportedHandler(conn, req)
			return
		}
		handleCamliUsingStorage(conn, req, action, storageConfig)
	})
}

func handleCamliUsingStorage(conn http.ResponseWriter, req *http.Request, action string, storage blobserver.StorageConfiger) {
	handler := unsupportedHandler
	switch req.Method {
	case "GET":
		switch action {
		case "enumerate-blobs":
			handler = auth.RequireAuth(handlers.CreateEnumerateHandler(storage))
		case "stat":
			handler = auth.RequireAuth(handlers.CreateStatHandler(storage))
		default:
			handler = handlers.CreateGetHandler(storage)
		}
	case "POST":
		switch action {
		case "stat":
			handler = auth.RequireAuth(handlers.CreateStatHandler(storage))
		case "upload":
			handler = auth.RequireAuth(handlers.CreateUploadHandler(storage))
		case "remove":
			handler = auth.RequireAuth(handlers.CreateRemoveHandler(storage))
		}
	case "PUT": // no longer part of spec
		handler = auth.RequireAuth(handlers.CreateNonStandardPutHandler(storage))
	}
	handler(conn, req)
}

func exitFailure(pattern string, args ...interface{}) {
	if !strings.HasSuffix(pattern, "\n") {
		pattern = pattern + "\n"
	}
	fmt.Fprintf(os.Stderr, pattern, args...)
	os.Exit(1)
}

type handlerConfig struct {
	prefix string         // "/foo/"
	htype  string         // "localdisk", etc
	conf   jsonconfig.Obj // never nil

	settingUp, setupDone bool
}

type handlerLoader struct {
	ws      *webserver.Server
	baseURL string
	config  map[string]*handlerConfig // prefix -> config
	handler map[string]interface{}    // prefix -> http.Handler / func
}

func main() {
	flag.Parse()

	configPath := *flagConfigFile
	if !filepath.IsAbs(configPath) {
		configPath = filepath.Join(osutil.CamliConfigDir(), configPath)
	}
	f, err := os.Open(configPath)
	if err != nil {
		exitFailure("error opening %s: %v", configPath, err)
	}
	defer f.Close()
	dj := json.NewDecoder(f)
	rootjson := make(map[string]interface{})
	if err = dj.Decode(&rootjson); err != nil {
		extra := ""
		if serr, ok := err.(*json.SyntaxError); ok {
			if _, serr := f.Seek(0, os.SEEK_SET); serr != nil {
				log.Fatalf("seek error: %v", serr)
			}
			line, col, highlight := errorutil.HighlightBytePosition(f, serr.Offset)
			extra = fmt.Sprintf(":\nError at line %d, column %d (file offset %d):\n%s",
				line, col, serr.Offset, highlight)
		}
		exitFailure("error parsing JSON object in config file %s%s\n%v",
			osutil.UserServerConfigPath(), extra, err)
	}
	if err := jsonconfig.EvaluateExpressions(rootjson); err != nil {
		exitFailure("error expanding JSON config expressions in %s: %v", configPath, err)
	}

	ws := webserver.New()
	baseURL := ws.BaseURL()

	// Root configuration
	config := jsonconfig.Obj(rootjson)

	{
		cert, key := config.OptionalString("TLSCertFile", ""), config.OptionalString("TLSKeyFile", "")
		if (cert != "") != (key != "") {
			exitFailure("TLSCertFile and TLSKeyFile must both be either present or absent")
		}
		if cert != "" {
			ws.SetTLS(cert, key)
		}
	}

	auth.AccessPassword = config.OptionalString("password", "")
	if url := config.OptionalString("baseURL", ""); url != "" {
		baseURL = url
	}
	prefixes := config.RequiredObject("prefixes")
	if err := config.Validate(); err != nil {
		exitFailure("configuration error in root object's keys in %s: %v", configPath, err)
	}

	hl := &handlerLoader{
		ws:      ws,
		baseURL: baseURL,
		config:  make(map[string]*handlerConfig),
		handler: make(map[string]interface{}),
	}

	for prefix, vei := range prefixes {
		if !strings.HasPrefix(prefix, "/") {
			exitFailure("prefix %q doesn't start with /", prefix)
		}
		if !strings.HasSuffix(prefix, "/") {
			exitFailure("prefix %q doesn't end with /", prefix)
		}
		pmap, ok := vei.(map[string]interface{})
		if !ok {
			exitFailure("prefix %q value isn't an object", prefix)
		}
		pconf := jsonconfig.Obj(pmap)
		handlerType := pconf.RequiredString("handler")
		handlerArgs := pconf.OptionalObject("handlerArgs")
		if err := pconf.Validate(); err != nil {
			exitFailure("configuration error in prefix %s: %v", prefix, err)
		}
		h := &handlerConfig{
			prefix: prefix,
			htype:  handlerType,
			conf:   handlerArgs,
		}
		hl.config[prefix] = h
	}
	hl.setupAll()
	ws.Serve()
}

func (hl *handlerLoader) setupAll() {
	for prefix := range hl.config {
		hl.setupHandler(prefix)
	}
}

func (hl *handlerLoader) configType(prefix string) string {
	if h, ok := hl.config[prefix]; ok {
		return h.htype
	}
	return ""
}

func (hl *handlerLoader) getOrSetup(prefix string) interface{} {
	hl.setupHandler(prefix)
	return hl.handler[prefix]
}

func (hl *handlerLoader) setupHandler(prefix string) {
	h, ok := hl.config[prefix]
	if !ok {
		exitFailure("invalid reference to non-existant handler %q", prefix)
	}
	if h.setupDone {
		// Already setup by something else reference it and forcing it to be
		// setup before the bottom loop got to it.
		return
	}
	if h.settingUp {
		exitFailure("loop in configuration graph; %q tried to load itself indirectly", prefix)
	}
	h.settingUp = true
	defer func() {
		h.setupDone = true
		if hl.handler[prefix] == nil {
			panic(fmt.Sprintf("setupHandler for %q didn't install a handler", prefix))
		}
	}()
	installHandler := func(creator func(_ *handlerLoader, conf jsonconfig.Obj) (h http.Handler, err os.Error)) {
		hh, err := creator(hl, h.conf)
		if err != nil {
			exitFailure("error instantiating handler for prefix %s: %v",
				prefix, err)
		}
		hl.handler[prefix] = hh
		hl.ws.Handle(prefix, &httputil.PrefixHandler{prefix, hh})
	}
	checkConfig := func() {
		if err := h.conf.Validate(); err != nil {
			exitFailure("configuration error in \"handlerArgs\" for prefix %s: %v", prefix, err)
		}
	}
	switch h.htype {
	case "root":
		installHandler((*handlerLoader).createRootHandler)
	case "ui":
		installHandler((*handlerLoader).createUIHandler)
	case "jsonsign":
		installHandler((*handlerLoader).createJSONSignHandler)
	case "search":
		indexPrefix := h.conf.RequiredString("index") // TODO: add optional help tips here?
		ownerBlobStr := h.conf.RequiredString("owner")
		checkConfig()
		indexer, ok := hl.getOrSetup(indexPrefix).(search.Index)
		if !ok {
			exitFailure("prefix %q references invalid indexer %q", prefix, indexPrefix)
		}
		ownerBlobRef := blobref.Parse(ownerBlobStr)
		if ownerBlobRef == nil {
			exitFailure("prefix %q references has malformed blobref %q; expecting e.g. sha1-xxxxxxxxxxxx",
				prefix, ownerBlobStr)
		}
		searchh := auth.RequireAuth(search.CreateHandler(indexer, ownerBlobRef))
		hl.handler[h.prefix] = searchh
		hl.ws.HandleFunc(prefix+"camli/", searchh)
	case "sync":
		from := h.conf.RequiredString("from")
		to := h.conf.RequiredString("to")
		checkConfig()
		getBlobServer := func(bsPrefix string) blobserver.Storage {
			bs, ok := hl.getOrSetup(bsPrefix).(blobserver.Storage)
			if !ok {
				exitFailure("sync prefix %q references %q, of type %T, but expected a blob server",
					prefix, bsPrefix, h)
			}
			return bs
		}
		fromBs, toBs := getBlobServer(from), getBlobServer(to)
		synch, err := createSyncHandler(from, to, fromBs, toBs)
		if err != nil {
			exitFailure(err.String())
		}
		hl.handler[h.prefix] = synch
		hl.ws.Handle(prefix, synch)
	default:
		// Assume a storage interface
		pstorage, err := blobserver.CreateStorage(h.htype, h.conf)
		if err != nil {
			exitFailure("error instantiating storage for prefix %q, type %q: %v",
				h.prefix, h.htype, err)
		}
		hl.handler[h.prefix] = pstorage
		hl.ws.Handle(prefix+"camli/", makeCamliHandler(prefix, hl.baseURL, pstorage))
	}
}
