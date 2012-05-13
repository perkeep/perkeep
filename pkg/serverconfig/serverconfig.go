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

package serverconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"camlistore.org/pkg/auth"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/handlers"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/jsonconfig"
)

const camliPrefix = "/camli/"

var ErrCamliPath = errors.New("Invalid Camlistore request path")

type handlerConfig struct {
	prefix string         // "/foo/"
	htype  string         // "localdisk", etc
	conf   jsonconfig.Obj // never nil

	settingUp, setupDone bool
}

type handlerLoader struct {
	installer HandlerInstaller
	baseURL   string
	config    map[string]*handlerConfig // prefix -> config
	handler   map[string]interface{}    // prefix -> http.Handler / func / blobserver.Storage

	// optional context (for App Engine, the first request that
	// started up the process).  we may need this if setting up
	// handlers involves doing datastore/memcache/blobstore
	// lookups.
	context *http.Request
}

// A HandlerInstaller is anything that can register an HTTP Handler at
// a prefix path.  Both *http.ServeMux and camlistore.org/pkg/webserver.Server
// implement HandlerInstaller.
type HandlerInstaller interface {
	Handle(path string, handler http.Handler)
}

type storageAndConfig struct {
	blobserver.Storage
	config *blobserver.Config
}

var _ blobserver.ContextWrapper = (*storageAndConfig)(nil)

func (sc *storageAndConfig) WrapContext(req *http.Request) blobserver.Storage {
	if w, ok := sc.Storage.(blobserver.ContextWrapper); ok {
		return &storageAndConfig{w.WrapContext(req), sc.config}
	}
	return sc
}

func parseCamliPath(path string) (action string, err error) {
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

func (s *storageAndConfig) Config() *blobserver.Config {
	return s.config
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

// where prefix is like "/" or "/s3/" for e.g. "/camli/" or "/s3/camli/*"
func makeCamliHandler(prefix, baseURL string, storage blobserver.Storage) http.Handler {
	if !strings.HasSuffix(prefix, "/") {
		panic("expected prefix to end in slash")
	}
	baseURL = strings.TrimRight(baseURL, "/")

	canLongPoll := true
	// TODO(bradfitz): set to false if this is App Engine, or provide some way to disable

	storageConfig := &storageAndConfig{
		storage,
		&blobserver.Config{
			Writable:    true,
			Readable:    true,
			IsQueue:     false,
			URLBase:     baseURL + prefix[:len(prefix)-1],
			CanLongPoll: canLongPoll,
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

func (hl *handlerLoader) GetRequestContext() (req *http.Request, ok bool) {
	return hl.context, hl.context != nil
}

func (hl *handlerLoader) FindHandlerByType(htype string) (prefix string, handler interface{}, err error) {
	for prefix, config := range hl.config {
		if config.htype == htype {
			return prefix, hl.handler[prefix], nil
		}
	}
	return "", nil, blobserver.ErrHandlerTypeNotFound
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

func (hl *handlerLoader) GetStorage(prefix string) (blobserver.Storage, error) {
	hl.setupHandler(prefix)
	if s, ok := hl.handler[prefix].(blobserver.Storage); ok {
		return s, nil
	}
	return nil, fmt.Errorf("bogus storage handler referenced as %q", prefix)
}

func (hl *handlerLoader) GetHandler(prefix string) (interface{}, error) {
	hl.setupHandler(prefix)
	if s, ok := hl.handler[prefix].(blobserver.Storage); ok {
		return s, nil
	}
	if h, ok := hl.handler[prefix].(http.Handler); ok {
		return h, nil
	}
	return nil, fmt.Errorf("bogus http or storage handler referenced as %q", prefix)
}

func (hl *handlerLoader) GetHandlerType(prefix string) string {
	hl.setupHandler(prefix)
	return hl.configType(prefix)
}

func exitFailure(pattern string, args ...interface{}) {
	if !strings.HasSuffix(pattern, "\n") {
		pattern = pattern + "\n"
	}
	panic(fmt.Sprintf(pattern, args...))
}

func (hl *handlerLoader) setupHandler(prefix string) {
	h, ok := hl.config[prefix]
	if !ok {
		exitFailure("invalid reference to undefined handler %q", prefix)
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
		r := recover()
		if r == nil {
			if hl.handler[prefix] == nil {
				panic(fmt.Sprintf("setupHandler for %q didn't install a handler", prefix))
			}
		} else {
			panic(r)
		}
	}()

	if strings.HasPrefix(h.htype, "storage-") {
		stype := h.htype[len("storage-"):]
		// Assume a storage interface
		pstorage, err := blobserver.CreateStorage(stype, hl, h.conf)
		if err != nil {
			exitFailure("error instantiating storage for prefix %q, type %q: %v",
				h.prefix, stype, err)
		}
		hl.handler[h.prefix] = pstorage
		hl.installer.Handle(prefix+"camli/", makeCamliHandler(prefix, hl.baseURL, pstorage))
		return
	}

	hh, err := blobserver.CreateHandler(h.htype, hl, h.conf)
	if err != nil {
		exitFailure("error instantiating handler for prefix %q, type %q: %v",
			h.prefix, h.htype, err)
	}
	hl.handler[prefix] = hh
	var wrappedHandler http.Handler = &httputil.PrefixHandler{prefix, hh}
	if handerTypeWantsAuth(h.htype) {
		wrappedHandler = auth.Handler{wrappedHandler}
	}
	hl.installer.Handle(prefix, wrappedHandler)
}

func handerTypeWantsAuth(handlerType string) bool {
	// TODO(bradfitz): ask the handler instead? This is a bit of a
	// weird spot for this policy maybe?
	switch handlerType {
	case "ui", "search", "jsonsign", "sync":
		return true
	}
	return false
}

type Config struct {
	jsonconfig.Obj
	UIPath     string // Not valid until after InstallHandlers
	configPath string // Filesystem path
}

// Load returns a low-level "handler config" from the provided filename.
// If the config file doesn't contain a top-level JSON key of "handlerConfig"
// with boolean value true, the configuration is assumed to be a high-level
// "user config" file, and transformed into a low-level config.
func Load(filename string) (*Config, error) {
	obj, err := jsonconfig.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	conf := &Config{
		Obj:        obj,
		configPath: filename,
	}

	if lowLevel := obj.OptionalBool("handlerConfig", false); !lowLevel {
		conf, err = GenLowLevelConfig(conf)
		if err != nil {
			return nil, fmt.Errorf(
				"Failed to transform user config file %q into internal handler configuration: %v",
				filename, err)
		}
		if v, _ := strconv.ParseBool(os.Getenv("CAMLI_DEBUG_CONFIG")); v {
			jsconf, _ := json.MarshalIndent(conf.Obj, "", "  ")
			log.Printf("From high-level config, generated low-level config: %s", jsconf)
		}
	}

	return conf, nil
}

func (config *Config) checkValidAuth() error {
	authConfig := config.OptionalString("auth", "")
	_, err := auth.FromConfig(authConfig)
	return err
}

// InstallHandlers creates and registers all the HTTP Handlers needed by config
// into the provided HandlerInstaller.
//
// baseURL is required and specifies the root of this webserver, without trailing slash.
// context may be nil (used and required by App Engine only)
func (config *Config) InstallHandlers(hi HandlerInstaller, baseURL string, context *http.Request) (outerr error) {
	defer func() {
		err := recover()
		if err == nil {
			return
		}
		outerr = fmt.Errorf("%v", err)
	}()

	if err := config.checkValidAuth(); err != nil {
		return fmt.Errorf("error while configuring auth: %v", err)
	}
	prefixes := config.RequiredObject("prefixes")
	if err := config.Validate(); err != nil {
		return fmt.Errorf("configuration error in root object's keys: %v", err)
	}

	hl := &handlerLoader{
		installer: hi,
		baseURL:   baseURL,
		config:    make(map[string]*handlerConfig),
		handler:   make(map[string]interface{}),
		context:   context,
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
			exitFailure("prefix %q value is a %T, not an object", prefix, vei)
		}
		pconf := jsonconfig.Obj(pmap)
		enabled := pconf.OptionalBool("enabled", true)
		if !enabled {
			continue
		}
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

		if handlerType == "ui" {
			config.UIPath = prefix
		}
	}
	hl.setupAll()
	return nil
}
