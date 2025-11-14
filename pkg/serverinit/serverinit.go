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

// Package serverinit is responsible for mapping from a Perkeep
// configuration file and instantiating HTTP Handlers for all the
// necessary endpoints.
package serverinit // import "perkeep.org/pkg/serverinit"

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"expvar"
	"fmt"
	"io"
	"log"
	"maps"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"reflect"
	"regexp"
	"runtime"
	"runtime/debug"
	rpprof "runtime/pprof"
	"strconv"
	"strings"

	"perkeep.org/internal/httputil"
	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/handlers"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/jsonsign/signhandler"
	"perkeep.org/pkg/server"
	"perkeep.org/pkg/server/app"
	"perkeep.org/pkg/types/serverconfig"

	"cloud.google.com/go/compute/metadata"
	"go4.org/jsonconfig"
)

const camliPrefix = "/camli/"

var ErrCamliPath = errors.New("invalid Perkeep request path")

type handlerConfig struct {
	prefix   string         // "/foo/"
	htype    string         // "localdisk", etc
	conf     jsonconfig.Obj // never nil
	internal bool           // if true, not accessible over HTTP

	settingUp, setupDone bool
}

// handlerLoader implements blobserver.Loader.
type handlerLoader struct {
	installer   HandlerInstaller
	baseURL     string
	config      map[string]*handlerConfig // prefix -> config
	handler     map[string]any            // prefix -> http.Handler / func / blobserver.Storage
	curPrefix   string
	closers     []io.Closer
	prefixStack []string
}

var _ blobserver.Loader = (*handlerLoader)(nil)

// A HandlerInstaller is anything that can register an HTTP Handler at
// a prefix path.  Both *http.ServeMux and perkeep.org/pkg/webserver.Server
// implement HandlerInstaller.
type HandlerInstaller interface {
	Handle(path string, h http.Handler)
}

type storageAndConfig struct {
	blobserver.Storage
	config *blobserver.Config
}

// parseCamliPath looks for "/camli/" in the path and returns
// what follows it (the action).
func parseCamliPath(path string) (action string, err error) {
	camIdx := strings.Index(path, camliPrefix)
	if camIdx == -1 {
		return "", ErrCamliPath
	}
	action = path[camIdx+len(camliPrefix):]
	return
}

func unsupportedHandler(rw http.ResponseWriter, req *http.Request) {
	httputil.BadRequestError(rw, "Unsupported Perkeep path or method.")
}

func (s *storageAndConfig) Config() *blobserver.Config {
	return s.config
}

// GetStorage returns the unwrapped blobserver.Storage interface value for
// callers to type-assert optional interface implementations on. (e.g. EnumeratorConfig)
func (s *storageAndConfig) GetStorage() blobserver.Storage {
	return s.Storage
}

// action is the part following "/camli/" in the URL. It's either a
// string like "enumerate-blobs", "stat", "upload", or a blobref.
func camliHandlerUsingStorage(req *http.Request, action string, storage blobserver.StorageConfiger) (http.Handler, auth.Operation) {
	var handler http.Handler
	op := auth.OpAll
	switch req.Method {
	case "GET", "HEAD":
		switch action {
		case "enumerate-blobs":
			handler = handlers.CreateEnumerateHandler(storage)
			op = auth.OpGet
		case "stat":
			handler = handlers.CreateStatHandler(storage)
		case "ws":
			handler = nil         // TODO: handlers.CreateSocketHandler(storage)
			op = auth.OpDiscovery // rest of operation auth checks done in handler
		default:
			handler = handlers.CreateGetHandler(storage)
			op = auth.OpGet
		}
	case "POST":
		switch action {
		case "stat":
			handler = handlers.CreateStatHandler(storage)
			op = auth.OpStat
		case "upload":
			handler = handlers.CreateBatchUploadHandler(storage)
			op = auth.OpUpload
		case "remove":
			handler = handlers.CreateRemoveHandler(storage)
		}
	case "PUT":
		handler = handlers.CreatePutUploadHandler(storage)
		op = auth.OpUpload
	}
	if handler == nil {
		handler = http.HandlerFunc(unsupportedHandler)
	}
	return handler, op
}

// where prefix is like "/" or "/s3/" for e.g. "/camli/" or "/s3/camli/*"
func makeCamliHandler(prefix, baseURL string, storage blobserver.Storage, hf blobserver.FindHandlerByTyper) http.Handler {
	if !strings.HasSuffix(prefix, "/") {
		panic("expected prefix to end in slash")
	}
	baseURL = strings.TrimRight(baseURL, "/")

	canLongPoll := true
	// TODO(bradfitz): set to false if this is App Engine, or provide some way to disable

	storageConfig := &storageAndConfig{
		storage,
		&blobserver.Config{
			Writable:      true,
			Readable:      true,
			Deletable:     false,
			URLBase:       baseURL + prefix[:len(prefix)-1],
			CanLongPoll:   canLongPoll,
			HandlerFinder: hf,
		},
	}
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		action, err := parseCamliPath(req.URL.Path[len(prefix)-1:])
		if err != nil {
			log.Printf("Invalid request for method %q, path %q",
				req.Method, req.URL.Path)
			unsupportedHandler(rw, req)
			return
		}
		handler := auth.RequireAuth(camliHandlerUsingStorage(req, action, storageConfig))
		handler.ServeHTTP(rw, req)
	})
}

func (hl *handlerLoader) FindHandlerByType(htype string) (prefix string, handler any, err error) {
	nFound := 0
	for pfx, config := range hl.config {
		if config.htype == htype {
			nFound++
			prefix, handler = pfx, hl.handler[pfx]
		}
	}
	if nFound == 0 {
		return "", nil, blobserver.ErrHandlerTypeNotFound
	}
	if htype == "jsonsign" && nFound > 1 {
		// TODO: do this for all handler types later? audit
		// callers of FindHandlerByType and see if that's
		// feasible. For now I'm only paranoid about jsonsign.
		return "", nil, fmt.Errorf("%d handlers found of type %q; ambiguous", nFound, htype)
	}
	return
}

func (hl *handlerLoader) AllHandlers() (types map[string]string, handlers map[string]any) {
	types = make(map[string]string)
	handlers = make(map[string]any)
	for pfx, config := range hl.config {
		types[pfx] = config.htype
		handlers[pfx] = hl.handler[pfx]
	}
	return
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

func (hl *handlerLoader) MyPrefix() string {
	return hl.curPrefix
}

func (hl *handlerLoader) BaseURL() string {
	return hl.baseURL
}

func (hl *handlerLoader) GetStorage(prefix string) (blobserver.Storage, error) {
	hl.setupHandler(prefix)
	if s, ok := hl.handler[prefix].(blobserver.Storage); ok {
		return s, nil
	}
	return nil, fmt.Errorf("bogus storage handler referenced as %q", prefix)
}

func (hl *handlerLoader) GetHandler(prefix string) (any, error) {
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
	return hl.configType(prefix)
}

func exitFailure(pattern string, args ...any) {
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
	hl.prefixStack = append(hl.prefixStack, prefix)
	if h.settingUp {
		buf := make([]byte, 1024)
		buf = buf[:runtime.Stack(buf, false)]
		exitFailure("loop in configuration graph; %q tried to load itself indirectly: %q\nStack:\n%s",
			prefix, hl.prefixStack, buf)
	}
	h.settingUp = true
	defer func() {
		// log.Printf("Configured handler %q", prefix)
		h.setupDone = true
		hl.prefixStack = hl.prefixStack[:len(hl.prefixStack)-1]
		r := recover()
		if r == nil {
			if hl.handler[prefix] == nil {
				panic(fmt.Sprintf("setupHandler for %q didn't install a handler", prefix))
			}
		} else {
			panic(r)
		}
	}()

	hl.curPrefix = prefix

	if after, ok0 := strings.CutPrefix(h.htype, "storage-"); ok0 {
		// Assume a storage interface:
		stype := after
		pstorage, err := blobserver.CreateStorage(stype, hl, h.conf)
		if err != nil {
			exitFailure("error instantiating storage for prefix %q, type %q: %v",
				h.prefix, stype, err)
		}
		if ix, ok := pstorage.(*index.Index); ok && ix.WantsReindex() {
			log.Printf("Reindexing %s ...", h.prefix)
			if err := ix.Reindex(); err != nil {
				if ix.WantsKeepGoing() {
					log.Printf("Error reindexing %s: %v", h.prefix, err)
				} else {
					exitFailure("Error reindexing %s: %v", h.prefix, err)
				}
			}
		}
		hl.handler[h.prefix] = pstorage
		if h.internal {
			hl.installer.Handle(prefix, unauthorizedHandler{})
		} else {
			hl.installer.Handle(prefix+"camli/", makeCamliHandler(prefix, hl.baseURL, pstorage, hl))
		}
		if cl, ok := pstorage.(blobserver.ShutdownStorage); ok {
			hl.closers = append(hl.closers, cl)
		}
		return
	}

	var hh http.Handler
	if h.htype == "app" {
		// h.conf might already contain the server's baseURL, but
		// perkeepd.go derives (if needed) a more useful hl.baseURL,
		// after h.conf was generated, so we provide it as well to
		// FromJSONConfig so NewHandler can benefit from it.
		hc, err := app.FromJSONConfig(h.conf, prefix, hl.baseURL)
		if err != nil {
			exitFailure("error setting up app config for prefix %q: %v", h.prefix, err)
		}
		ap, err := app.NewHandler(hc)
		if err != nil {
			exitFailure("error setting up app for prefix %q: %v", h.prefix, err)
		}
		hh = ap
		auth.AddMode(ap.AuthMode())
		// TODO(mpl): this check is weak, as the user could very well
		// use another binary name for the publisher app. We should
		// introduce/use another identifier.
		if ap.ProgramName() == "publisher" {
			if err := hl.initPublisherRootNode(ap); err != nil {
				exitFailure("Error looking/setting up root node for publisher on %v: %v", h.prefix, err)
			}
		}
	} else {
		var err error
		hh, err = blobserver.CreateHandler(h.htype, hl, h.conf)
		if err != nil {
			exitFailure("error instantiating handler for prefix %q, type %q: %v",
				h.prefix, h.htype, err)
		}
	}

	hl.handler[prefix] = hh
	var wrappedHandler http.Handler
	if h.internal {
		wrappedHandler = unauthorizedHandler{}
	} else {
		wrappedHandler = &httputil.PrefixHandler{Prefix: prefix, Handler: hh}
		if handlerTypeWantsAuth(h.htype) {
			wrappedHandler = auth.Handler{Handler: wrappedHandler}
		}
	}
	hl.installer.Handle(prefix, wrappedHandler)
}

type unauthorizedHandler struct{}

func (unauthorizedHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Unauthorized", http.StatusUnauthorized)
}

func handlerTypeWantsAuth(handlerType string) bool {
	// TODO(bradfitz): ask the handler instead? This is a bit of a
	// weird spot for this policy maybe?
	switch handlerType {
	case "ui", "search", "jsonsign", "sync", "status", "help", "importer":
		return true
	}
	return false
}

// A Config is the wrapper around a Perkeep JSON configuration file.
// Files on disk can be in either high-level or low-level format, but
// the Load function always returns the Config in its low-level format
// (a.k.a. the "handler" format).
//
// TODO(bradfitz): document and/or link to the low-level format; for
// now you can see the high-level config format at https://perkeep.org/pkg/types/serverconfig/#Config
// and the the low-level format by running "camtool dumpconfig".
type Config struct {
	jconf jsonconfig.Obj // low-level JSON config

	httpsCert  string // optional
	httpsKey   string // optional
	https      bool
	baseURL    string // optional, without trailing slash
	listenAddr string // the optional net.Listen-style TCP listen address

	installedHandlers bool   // whether InstallHandlers (which validates the config too) has been called
	uiPath            string // Not valid until after InstallHandlers

	// apps is the list of server apps configured during InstallHandlers,
	// and that should be started after perkeepd has started serving.
	apps []*app.Handler
	// signHandler is found and configured during InstallHandlers, or nil.
	// It is stored in the Config, so we can call UploadPublicKey on on it as
	// soon as perkeepd is ready for it.
	signHandler *signhandler.Handler
}

// UIPath returns the relative path to the server's user interface
// handler, if the UI is configured. Otherwise it returns the empty
// string. It is not valid until after a call to InstallHandlers
//
// If non-empty, the returned value will both begin and end with a slash.
func (c *Config) UIPath() string {
	if !c.installedHandlers {
		panic("illegal UIPath call before call to InstallHandlers")
	}
	return c.uiPath
}

// BaseURL returns the optional URL prefix listening the root of this server.
// It does not end in a trailing slash.
func (c *Config) BaseURL() string { return c.baseURL }

// ListenAddr returns the optional configured listen address in ":port" or "ip:port" form.
func (c *Config) ListenAddr() string { return c.listenAddr }

// HTTPSCert returns the optional path to an HTTPS public key certificate file.
func (c *Config) HTTPSCert() string { return c.httpsCert }

// HTTPSKey returns the optional path to an HTTPS private key file.
func (c *Config) HTTPSKey() string { return c.httpsKey }

// HTTPS reports whether this configuration wants to serve HTTPS.
func (c *Config) HTTPS() bool { return c.https }

// IsTailscaleListener reports whether c is configured to run in
// Tailscale tsnet mode.
func (c *Config) IsTailscaleListener() bool {
	return c.listenAddr == "tailscale" || strings.HasPrefix(c.listenAddr, "tailscale:")
}

// detectConfigChange returns an informative error if conf contains obsolete keys.
func detectConfigChange(conf jsonconfig.Obj) error {
	oldHTTPSKey, oldHTTPSCert := conf.OptionalString("HTTPSKeyFile", ""), conf.OptionalString("HTTPSCertFile", "")
	if oldHTTPSKey != "" || oldHTTPSCert != "" {
		return fmt.Errorf("config keys %q and %q have respectively been renamed to %q and %q, please fix your server config",
			"HTTPSKeyFile", "HTTPSCertFile", "httpsKey", "httpsCert")
	}
	return nil
}

// LoadFile returns a low-level "handler config" from the provided filename.
// If the config file doesn't contain a top-level JSON key of "handlerConfig"
// with boolean value true, the configuration is assumed to be a high-level
// "user config" file, and transformed into a low-level config.
func LoadFile(filename string) (*Config, error) {
	return load(filename, nil)
}

// jsonFileImpl implements jsonconfig.File using a *bytes.Reader with
// the contents slurped into memory.
type jsonFileImpl struct {
	*bytes.Reader
	name string
}

func (jsonFileImpl) Close() error   { return nil }
func (f jsonFileImpl) Name() string { return f.name }

// Load returns a low-level "handler config" from the provided config.
// If the config doesn't contain a top-level JSON key of "handlerConfig"
// with boolean value true, the configuration is assumed to be a high-level
// "user config" file, and transformed into a low-level config.
func Load(config []byte) (*Config, error) {
	return load("", func(filename string) (jsonconfig.File, error) {
		if filename != "" {
			return nil, errors.New("JSON files with includes not supported with jsonconfig.Load")
		}
		return jsonFileImpl{bytes.NewReader(config), "config file"}, nil
	})
}

func load(filename string, opener func(filename string) (jsonconfig.File, error)) (*Config, error) {
	c := osutil.NewJSONConfigParser()
	c.Open = opener
	m, err := c.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	obj := jsonconfig.Obj(m)
	conf := &Config{
		jconf: obj,
	}

	if lowLevel := obj.OptionalBool("handlerConfig", false); lowLevel {
		if err := conf.readFields(); err != nil {
			return nil, err
		}
		return conf, nil
	}

	// Check whether the high-level config uses the old names.
	if err := detectConfigChange(obj); err != nil {
		return nil, err
	}

	// Because the original high-level config might have expanded
	// through the use of functions, we re-encode the map back to
	// JSON here so we can unmarshal it into the hiLevelConf
	// struct later.
	highExpandedJSON, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("Can't re-marshal high-level JSON config: %v", err)
	}

	var hiLevelConf serverconfig.Config
	if err := json.Unmarshal(highExpandedJSON, &hiLevelConf); err != nil {
		return nil, fmt.Errorf("Could not unmarshal into a serverconfig.Config: %v", err)
	}

	// At this point, conf.jconf.UnknownKeys() contains all the names found in
	// the given high-level configuration. We check them against
	// highLevelConfFields(), which gives us all the possible valid
	// configuration names, to catch typos or invalid names.
	allFields := highLevelConfFields()
	for _, v := range conf.jconf.UnknownKeys() {
		if _, ok := allFields[v]; !ok {
			return nil, fmt.Errorf("unknown high-level configuration parameter: %q in file %q", v, filename)
		}
	}
	conf, err = genLowLevelConfig(&hiLevelConf)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to transform user config file into internal handler configuration: %v",
			err)
	}
	if v, _ := strconv.ParseBool(os.Getenv("CAMLI_DEBUG_CONFIG")); v {
		jsconf, _ := json.MarshalIndent(conf.jconf, "", "  ")
		log.Printf("From high-level config, generated low-level config: %s", jsconf)
	}
	if err := conf.readFields(); err != nil {
		return nil, err
	}
	return conf, nil
}

// readFields reads the low-level jsonconfig fields using the jsonconfig package
// and copies them into c. This marks them as known fields before a future call to InstallerHandlers
func (c *Config) readFields() error {
	c.listenAddr = c.jconf.OptionalString("listen", "")
	c.baseURL = strings.TrimSuffix(c.jconf.OptionalString("baseURL", ""), "/")
	c.httpsCert = c.jconf.OptionalString("httpsCert", "")
	c.httpsKey = c.jconf.OptionalString("httpsKey", "")
	c.https = c.jconf.OptionalBool("https", false)

	_, explicitHTTPS := c.jconf["https"]
	if c.httpsCert != "" && !explicitHTTPS {
		return errors.New("httpsCert specified but https was not")
	}
	if c.httpsKey != "" && !explicitHTTPS {
		return errors.New("httpsKey specified but https was not")
	}
	return nil
}

func (c *Config) checkValidAuth() error {
	authConfig := c.jconf.OptionalString("auth", "")
	mode, err := auth.FromConfig(authConfig)
	if err == nil {
		auth.SetMode(mode)
	}
	return err
}

func (c *Config) SetReindex(v bool) {
	prefixes, _ := c.jconf["prefixes"].(map[string]any)
	for prefix, vei := range prefixes {
		if prefix == "_knownkeys" {
			continue
		}
		pmap, ok := vei.(map[string]any)
		if !ok {
			continue
		}
		pconf := jsonconfig.Obj(pmap)
		typ, _ := pconf["handler"].(string)
		if typ != "storage-index" {
			continue
		}
		opts, ok := pconf["handlerArgs"].(map[string]any)
		if ok {
			opts["reindex"] = v
		}
	}
}

// SetKeepGoing changes each configured prefix to set "keepGoing" to true. This
// indicates that validation, reindexing, or recovery behavior should not cause
// the process to end.
func (c *Config) SetKeepGoing(v bool) {
	prefixes, _ := c.jconf["prefixes"].(map[string]any)
	for prefix, vei := range prefixes {
		if prefix == "_knownkeys" {
			continue
		}
		pmap, ok := vei.(map[string]any)
		if !ok {
			continue
		}
		pconf := jsonconfig.Obj(pmap)
		typ, _ := pconf["handler"].(string)
		if typ != "storage-index" && typ != "storage-blobpacked" {
			continue
		}
		opts, ok := pconf["handlerArgs"].(map[string]any)
		if ok {
			opts["keepGoing"] = v
		}
	}
}

// InstallHandlers creates and registers all the HTTP Handlers needed
// by config into the provided HandlerInstaller and validates that the
// configuration is valid.
//
// baseURL is required and specifies the root of this webserver, without trailing slash.
//
// The returned shutdown value can be used to cleanly shut down the
// handlers.
func (c *Config) InstallHandlers(hi HandlerInstaller, baseURL string) (shutdown io.Closer, err error) {
	config := c
	defer func() {
		if e := recover(); e != nil {
			log.Printf("Caught panic installer handlers: %v", e)
			debug.PrintStack()
			err = fmt.Errorf("Caught panic: %v", e)
		}
	}()

	if err := config.checkValidAuth(); err != nil {
		return nil, fmt.Errorf("error while configuring auth: %v", err)
	}
	prefixes := config.jconf.RequiredObject("prefixes")
	if err := config.jconf.Validate(); err != nil {
		return nil, fmt.Errorf("configuration error in root object's keys: %v", err)
	}

	if v := os.Getenv("CAMLI_PPROF_START"); v != "" {
		cpuf := mustCreate(v + ".cpu")
		defer cpuf.Close()
		memf := mustCreate(v + ".mem")
		defer memf.Close()
		rpprof.StartCPUProfile(cpuf)
		defer rpprof.StopCPUProfile()
		defer rpprof.WriteHeapProfile(memf)
	}

	hl := &handlerLoader{
		installer: hi,
		baseURL:   baseURL,
		config:    make(map[string]*handlerConfig),
		handler:   make(map[string]any),
	}

	for prefix, vei := range prefixes {
		if prefix == "_knownkeys" {
			continue
		}
		if !strings.HasPrefix(prefix, "/") {
			exitFailure("prefix %q doesn't start with /", prefix)
		}
		if !strings.HasSuffix(prefix, "/") {
			exitFailure("prefix %q doesn't end with /", prefix)
		}
		pmap, ok := vei.(map[string]any)
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
		internal := pconf.OptionalBool("internal", false)
		if err := pconf.Validate(); err != nil {
			exitFailure("configuration error in prefix %s: %v", prefix, err)
		}
		h := &handlerConfig{
			prefix:   prefix,
			htype:    handlerType,
			conf:     handlerArgs,
			internal: internal,
		}
		hl.config[prefix] = h

		if handlerType == "ui" {
			config.uiPath = prefix
		}
	}
	hl.setupAll()

	// Now that everything is setup, run any handlers' InitHandler
	// methods.
	// And register apps that will be started later.
	for pfx, handler := range hl.handler {
		if starter, ok := handler.(*app.Handler); ok {
			config.apps = append(config.apps, starter)
		}
		if helpHandler, ok := handler.(*server.HelpHandler); ok {
			helpHandler.SetServerConfig(config.jconf)
		}
		if signHandler, ok := handler.(*signhandler.Handler); ok {
			config.signHandler = signHandler
		}
		if in, ok := handler.(blobserver.HandlerIniter); ok {
			if err := in.InitHandler(hl); err != nil {
				return nil, fmt.Errorf("Error calling InitHandler on %s: %v", pfx, err)
			}
		}
	}

	if v, _ := strconv.ParseBool(os.Getenv("CAMLI_HTTP_EXPVAR")); v {
		hi.Handle("/debug/vars", expvarHandler{})
	}
	if v, _ := strconv.ParseBool(os.Getenv("CAMLI_HTTP_PPROF")); v {
		hi.Handle("/debug/pprof/", profileHandler{})
	}
	hi.Handle("/debug/goroutines", auth.RequireAuth(http.HandlerFunc(dumpGoroutines), auth.OpRead))
	hi.Handle("/debug/config", auth.RequireAuth(configHandler{config}, auth.OpAll))
	hi.Handle("/debug/logs/", auth.RequireAuth(http.HandlerFunc(logsHandler), auth.OpAll))
	config.installedHandlers = true
	return multiCloser(hl.closers), nil
}

func dumpGoroutines(w http.ResponseWriter, r *http.Request) {
	buf := make([]byte, 2<<20)
	buf = buf[:runtime.Stack(buf, true)]
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(buf)
}

// StartApps starts all the server applications that were configured
// during InstallHandlers. It should only be called after perkeepd
// has started serving, since these apps might request some configuration
// from Perkeep to finish initializing.
func (c *Config) StartApps() error {
	for _, ap := range c.apps {
		if err := ap.Start(); err != nil {
			return fmt.Errorf("error starting app %v: %v", ap.ProgramName(), err)
		}
	}
	return nil
}

// UploadPublicKey uploads the public key blob with the sign handler that was
// configured during InstallHandlers.
func (c *Config) UploadPublicKey(ctx context.Context) error {
	if c.signHandler == nil {
		return nil
	}
	return c.signHandler.UploadPublicKey(ctx)
}

// AppURL returns a map of app name to app base URL for all the configured
// server apps.
func (c *Config) AppURL() map[string]string {
	appURL := make(map[string]string, len(c.apps))
	for _, ap := range c.apps {
		appURL[ap.ProgramName()] = ap.BackendURL()
	}
	return appURL
}

func mustCreate(path string) *os.File {
	f, err := os.Create(path)
	if err != nil {
		log.Fatalf("Failed to create %s: %v", path, err)
	}
	return f
}

type multiCloser []io.Closer

func (s multiCloser) Close() (err error) {
	for _, cl := range s {
		if err1 := cl.Close(); err == nil && err1 != nil {
			err = err1
		}
	}
	return
}

// expvarHandler publishes expvar stats.
type expvarHandler struct{}

func (expvarHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	fmt.Fprintf(w, "{\n")
	first := true
	expvar.Do(func(kv expvar.KeyValue) {
		if !first {
			fmt.Fprintf(w, ",\n")
		}
		first = false
		fmt.Fprintf(w, "%q: %s", kv.Key, kv.Value)
	})
	fmt.Fprintf(w, "\n}\n")
}

type configHandler struct {
	c *Config
}

var (
	knownKeys     = regexp.MustCompile(`(?ms)^\s+"_knownkeys": {.+?},?\n`)
	sensitiveLine = regexp.MustCompile(`(?m)^\s+\"(auth|aws_secret_access_key|password|client_secret|application_key|passphrase)\": "[^\"]+".*\n`)
	trailingComma = regexp.MustCompile(`,(\n\s*\})`)
)

func (h configHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	b, _ := json.MarshalIndent(h.c.jconf, "", "    ")
	b = knownKeys.ReplaceAll(b, nil)
	b = trailingComma.ReplaceAll(b, []byte("$1"))
	b = sensitiveLine.ReplaceAllFunc(b, func(ln []byte) []byte {
		i := bytes.IndexByte(ln, ':')
		r := string(ln[:i+1]) + ` "REDACTED"`
		if bytes.HasSuffix(bytes.TrimSpace(ln), []byte{','}) {
			r += ","
		}
		return []byte(r + "\n")
	})
	w.Write(b)
}

// profileHandler publishes server profile information.
type profileHandler struct{}

func (profileHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	switch req.URL.Path {
	case "/debug/pprof/cmdline":
		pprof.Cmdline(rw, req)
	case "/debug/pprof/profile":
		pprof.Profile(rw, req)
	case "/debug/pprof/symbol":
		pprof.Symbol(rw, req)
	default:
		pprof.Index(rw, req)
	}
}

func logsHandler(w http.ResponseWriter, r *http.Request) {
	suffix := strings.TrimPrefix(r.URL.Path, "/debug/logs/")
	switch suffix {
	case "perkeepd":
		projID, err := metadata.ProjectID()
		if err != nil {
			httputil.ServeError(w, r, fmt.Errorf("Error getting project ID: %v", err))
			return
		}
		http.Redirect(w, r,
			"https://console.developers.google.com/logs?project="+projID+"&service=custom.googleapis.com&logName=perkeepd-stderr",
			http.StatusFound)
	case "system":
		c := &http.Client{
			Transport: &http.Transport{
				Dial: func(network, addr string) (net.Conn, error) {
					return net.Dial("unix", "/run/camjournald.sock")
				},
			},
		}
		res, err := c.Get("http://journal/entries")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.Copy(w, res.Body)
	default:
		http.Error(w, "no such logs", 404)
	}
}

// highLevelConfFields returns all the possible fields of a serverconfig.Config,
// in their JSON form. This allows checking that the parameters in the high-level
// server configuration file are at least valid names, which is useful to catch
// typos.
func highLevelConfFields() map[string]bool {
	knownFields := make(map[string]bool)
	var c serverconfig.Config
	s := reflect.ValueOf(&c).Elem()
	for i := 0; i < s.NumField(); i++ {
		f := s.Type().Field(i)
		jsonTag, ok := f.Tag.Lookup("json")
		if !ok {
			panic(fmt.Sprintf("%q field in serverconfig.Config does not have a json tag", f.Name))
		}
		jsonFields := strings.Split(strings.TrimSuffix(strings.TrimPrefix(jsonTag, `"`), `"`), ",")
		jsonName := jsonFields[0]
		if jsonName == "" {
			panic(fmt.Sprintf("no json field name for %q field in serverconfig.Config", f.Name))
		}
		knownFields[jsonName] = true
	}
	return knownFields
}

// KeyRingAndId returns the GPG identity keyring path and the user's
// GPG keyID (TODO: length/case), if configured. TODO: which error
// value if not configured?
func (c *Config) KeyRingAndId() (keyRing, keyId string, err error) {
	prefixes := c.jconf.RequiredObject("prefixes")
	if len(prefixes) == 0 {
		return "", "", fmt.Errorf("no prefixes object in config")
	}
	sighelper := prefixes.OptionalObject("/sighelper/")
	if len(sighelper) == 0 {
		return "", "", fmt.Errorf("no sighelper object in prefixes")
	}
	handlerArgs := sighelper.OptionalObject("handlerArgs")
	if len(handlerArgs) == 0 {
		return "", "", fmt.Errorf("no handlerArgs object in sighelper")
	}
	keyId = handlerArgs.OptionalString("keyId", "")
	if keyId == "" {
		return "", "", fmt.Errorf("no keyId in sighelper")
	}
	keyRing = handlerArgs.OptionalString("secretRing", "")
	if keyRing == "" {
		return "", "", fmt.Errorf("no secretRing in sighelper")
	}
	return keyRing, keyId, nil
}

// LowLevelJSONConfig returns the config's underlying low-level JSON form
// for debugging.
//
// Deprecated: this is provided for debugging only and will be going away
// as the move to TOML-based configuration progresses. Do not depend on this.
func (c *Config) LowLevelJSONConfig() map[string]any {
	// Make a shallow clone of c.jconf so we can mutate the
	// handlerConfig key to make it explicitly true, without
	// modifying anybody's else view of it.
	ret := map[string]any{}
	maps.Copy(ret, c.jconf)
	ret["handlerConfig"] = true
	return ret
}
