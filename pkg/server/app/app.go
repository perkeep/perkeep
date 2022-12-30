/*
Copyright 2014 The Perkeep Authors.

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

// Package app helps with configuring and starting server applications
// from Perkeep.
// See also https://perkeep.org/doc/app-environment for the related
// variables.
package app // import "perkeep.org/pkg/server/app"

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	camhttputil "perkeep.org/internal/httputil"
	"perkeep.org/internal/netutil"
	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/search"

	"go4.org/jsonconfig"
)

// Handler acts as a reverse proxy for a server application started by
// Perkeep. It can also serve some extra JSON configuration to the app.
// In addition, the handler can be used as a limited search handler proxy.
type Handler struct {
	name    string            // Name of the app's program.
	envVars map[string]string // Variables set in the app's process environment. See doc/app-environment.txt.

	auth      auth.AuthMode   // Used for basic HTTP authenticating against the app requests.
	appConfig jsonconfig.Obj  // Additional parameters the app can request, or nil.
	sh        *search.Handler // or nil, if !hasSearch.

	masterQueryMu sync.RWMutex // guards two following fields
	// masterQuery is the search query that defines domainBlobs. If nil, no
	// search query is accepted by the search handler.
	masterQuery *search.SearchQuery
	// domainBlobs is the set of blobs allowed for search queries. If a
	// search query response includes at least one blob that is not in
	// domainBlobs, the query is rejected.
	domainBlobs        map[blob.Ref]bool
	domainBlobsRefresh time.Time // last time the domainBlobs were refreshed

	// Prefix is the URL path prefix where the app handler is mounted on
	// Perkeep, stripped of its trailing slash. Examples:
	// "/pics", "/blog".
	prefix             string
	proxy              *httputil.ReverseProxy // For redirecting requests to the app.
	backendURL         string                 // URL that we proxy to (i.e. base URL of the app).
	configURLPath      string                 // URL path for serving appConfig
	masterqueryURLPath string                 // URL path for setting the master query

	process *os.Process // The app's Pid. To send it signals on restart, etc.
}

func (a *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == a.masterqueryURLPath {
		a.handleMasterQuery(w, r)
		return
	}
	if a.configURLPath != "" && r.URL.Path == a.configURLPath {
		if a.auth.AllowedAccess(r)&auth.OpGet == auth.OpGet {
			camhttputil.ReturnJSON(w, a.appConfig)
		} else {
			auth.SendUnauthorized(w, r)
		}
		return
	}
	trimmedPath := strings.TrimPrefix(r.URL.Path, a.prefix)
	if strings.HasPrefix(trimmedPath, "/search") {
		a.handleSearch(w, r)
		return
	}

	if a.proxy == nil {
		http.Error(w, "no proxy for the app", 500)
		return
	}
	a.proxy.ServeHTTP(w, r)
}

// handleMasterQuery allows an app to register the master query that defines the
// domain limiting all subsequent search queries.
func (a *Handler) handleMasterQuery(w http.ResponseWriter, r *http.Request) {
	if !(a.auth.AllowedAccess(r)&auth.OpAll == auth.OpAll) {
		auth.SendUnauthorized(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "not a POST", http.StatusMethodNotAllowed)
		return
	}
	if a.sh == nil {
		http.Error(w, "app proxy has no search handler", 500)
		return
	}
	if refresh, _ := strconv.ParseBool(r.FormValue("refresh")); refresh {
		if err := a.refreshDomainBlobs(); err != nil {
			if err == errRefreshSuppress {
				http.Error(w, "too many refresh requests", http.StatusTooManyRequests)
			} else {
				http.Error(w, fmt.Sprintf("%v", err), 500)
			}
			return
		}
		w.Write([]byte("OK"))
		return
	}
	sq := new(search.SearchQuery)
	if err := sq.FromHTTP(r); err != nil {
		http.Error(w, fmt.Sprintf("error reading master query: %v", err), 500)
		return
	}
	var masterQuery search.SearchQuery = *(sq)
	masterQuery.Describe = masterQuery.Describe.Clone()
	sr, err := a.sh.Query(r.Context(), sq)
	if err != nil {
		http.Error(w, fmt.Sprintf("error running master query: %v", err), 500)
		return
	}
	a.masterQueryMu.Lock()
	defer a.masterQueryMu.Unlock()
	a.masterQuery = &masterQuery
	a.domainBlobs = make(map[blob.Ref]bool, len(sr.Describe.Meta))
	for _, v := range sr.Describe.Meta {
		a.domainBlobs[v.BlobRef] = true
	}
	a.domainBlobsRefresh = time.Now()
	w.Write([]byte("OK"))
}

var errRefreshSuppress = errors.New("refresh request suppressed")

func (a *Handler) refreshDomainBlobs() error {
	a.masterQueryMu.Lock()
	defer a.masterQueryMu.Unlock()
	if time.Now().Before(a.domainBlobsRefresh.Add(time.Minute)) {
		// suppress refresh request to no more than once per minute
		return errRefreshSuppress
	}
	if a.masterQuery == nil {
		return errors.New("no master query")
	}
	var sq search.SearchQuery = *(a.masterQuery)
	sq.Describe = sq.Describe.Clone()
	sr, err := a.sh.Query(context.TODO(), &sq)
	if err != nil {
		return fmt.Errorf("error running master query: %v", err)
	}
	a.domainBlobs = make(map[blob.Ref]bool, len(sr.Describe.Meta))
	for _, v := range sr.Describe.Meta {
		a.domainBlobs[v.BlobRef] = true
	}
	a.domainBlobsRefresh = time.Now()
	return nil
}

// handleSearch runs the requested search query against the search handler, and
// if the results are within the domain allowed by the master query, forwards them
// back to the client.
func (a *Handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		camhttputil.BadRequestError(w, camhttputil.InvalidMethodError{}.Error())
		return
	}
	if a.sh == nil {
		http.Error(w, "app proxy has no search handler", 500)
		return
	}
	a.masterQueryMu.RLock()
	if a.masterQuery == nil {
		http.Error(w, "search is not allowed", http.StatusForbidden)
		a.masterQueryMu.RUnlock()
		return
	}
	a.masterQueryMu.RUnlock()
	var sq search.SearchQuery
	if err := sq.FromHTTP(r); err != nil {
		camhttputil.ServeJSONError(w, err)
		return
	}
	sr, err := a.sh.Query(r.Context(), &sq)
	if err != nil {
		camhttputil.ServeJSONError(w, err)
		return
	}
	// check this search is in the allowed domain
	if !a.allowProxySearchResponse(sr) {
		// there's a chance our domainBlobs cache is expired so let's
		// refresh it and retry, but no more than once per minute.
		if err := a.refreshDomainBlobs(); err != nil {
			http.Error(w, "search scope is forbidden", http.StatusForbidden)
			return
		}
		if !a.allowProxySearchResponse(sr) {
			http.Error(w, "search scope is forbidden", http.StatusForbidden)
			return
		}
	}
	camhttputil.ReturnJSON(w, sr)
}

// allowProxySearchResponse checks whether the blobs in sr are within the domain
// defined by the masterQuery, and hence if the client is allowed to get that
// response.
func (a *Handler) allowProxySearchResponse(sr *search.SearchResult) bool {
	a.masterQueryMu.RLock()
	defer a.masterQueryMu.RUnlock()
	for _, v := range sr.Blobs {
		if _, ok := a.domainBlobs[v.Blob]; !ok {
			return false
		}
	}
	return true
}

// randListen returns the concatenation of the host part of listenAddr with a random port.
func randListen(listenAddr string) (string, error) {
	return randListenFn(listenAddr, netutil.RandPort)
}

// randListenFn only exists to allow testing of randListen, by letting the caller
// replace randPort with a func that actually has a predictable result.
func randListenFn(listenAddr string, randPortFn func() (int, error)) (string, error) {
	portIdx := strings.LastIndex(listenAddr, ":") + 1
	if portIdx <= 0 || portIdx >= len(listenAddr) {
		return "", errors.New("invalid listen addr, no port found")
	}
	port, err := randPortFn()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%d", listenAddr[:portIdx], port), nil
}

var portMap = map[string]string{
	"http":  "80",
	"https": "443",
}

// baseURL returns the concatenation of the scheme and host parts of
// serverBaseURL with the port of listenAddr.
func baseURL(serverBaseURL, listenAddr string) (string, error) {
	backendURL, err := url.Parse(serverBaseURL)
	if err != nil {
		return "", fmt.Errorf("invalid baseURL %q: %v", serverBaseURL, err)
	}
	scheme := backendURL.Scheme
	host := backendURL.Host
	if netutil.HasPort(host) {
		host = host[:strings.LastIndex(host, ":")]
	}
	port := portMap[scheme]
	if netutil.HasPort(listenAddr) {
		port = listenAddr[strings.LastIndex(listenAddr, ":")+1:]
	}
	return fmt.Sprintf("%s://%s:%s/", scheme, host, port), nil
}

// TODO(mpl): some way to avoid the redundancy with serverconfig.App would be
// nice. But at least HandlerConfig and its doc is cleaner than having to document a
// jsonconfig.Obj.

// HandlerConfig holds the configuration for an app Handler. See
// https://perkeep.org/doc/app-environment for the corresponding environment
// variables. If developing an app, see FromJSONConfig and NewHandler for details
// on where defaults are applied.
type HandlerConfig struct {
	// Program is the file name of the server app's program executable. Either
	// an absolute path, or the name of a file located in CAMLI_APP_BINDIR or in PATH.
	Program string `json:"program"`

	// Prefix is the URL path prefix on APIHost where the app handler is mounted.
	// It always ends with a trailing slash. Examples: "/pics/", "/blog/".
	// Defaults to the Perkeep URL path prefix for this app handler.
	Prefix string `json:"prefix,omitempty"`

	// Listen is the address (of the form host|ip:port) on which the app
	// will listen. It defines CAMLI_APP_LISTEN.
	// If empty, the default is the concatenation of ServerListen's host
	// part and a random port.
	Listen string `json:"listen,omitempty"`

	// ServerListen is the Perkeep server's listen address. Defaults to
	// the ServerBaseURL host part.
	ServerListen string `json:"serverListen,omitempty"`

	// BackendURL is the URL of the application's process, always ending in a
	// trailing slash. It is the URL that the app handler will proxy to when
	// getting requests for the concerned app.
	// If empty, the default is the concatenation of the ServerBaseURL
	// scheme, the ServerBaseURL host part, and the port of Listen.
	BackendURL string `json:"backendURL,omitempty"`

	// ServerBaseURL is the Perkeep server's BaseURL. Defaults to the
	// BaseURL value in the Perkeep server configuration.
	ServerBaseURL string `json:"serverBaseURL,omitempty"`

	// APIHost is the URL of the Perkeep server which the app should
	// use to make API calls. It always ends in a trailing slash. It defines CAMLI_API_HOST.
	// If empty, the default is ServerBaseURL, with a trailing slash appended.
	APIHost string `json:"apiHost,omitempty"`

	// AppConfig contains some additional configuration specific to each app.
	// See CAMLI_APP_CONFIG_URL.
	AppConfig jsonconfig.Obj
}

// FromJSONConfig creates an HandlerConfig from the contents of config.
// prefix and serverBaseURL are used if not found in config.
func FromJSONConfig(config jsonconfig.Obj, prefix, serverBaseURL string) (HandlerConfig, error) {
	hc := HandlerConfig{
		Program:       config.RequiredString("program"),
		Prefix:        config.OptionalString("prefix", prefix),
		BackendURL:    config.OptionalString("backendURL", ""),
		Listen:        config.OptionalString("listen", ""),
		APIHost:       config.OptionalString("apiHost", ""),
		ServerListen:  config.OptionalString("serverListen", ""),
		ServerBaseURL: config.OptionalString("serverBaseURL", serverBaseURL),
		AppConfig:     config.OptionalObject("appConfig"),
	}
	if err := config.Validate(); err != nil {
		return HandlerConfig{}, err
	}
	return hc, nil
}

// NewHandler creates a new handler from the given HandlerConfig. Two exceptions
// apply to the HandlerConfig documentation: NewHandler does not create default
// values for Prefix and ServerBaseURL. Prefix should be provided, and
// ServerBaseURL might be needed, depending on the other fields.
func NewHandler(cfg HandlerConfig) (*Handler, error) {
	if cfg.Program == "" {
		return nil, fmt.Errorf("app: could not initialize Handler: empty Program")
	}
	name := cfg.Program

	if cfg.Prefix == "" {
		return nil, fmt.Errorf("app: could not initialize Handler for %q: empty Prefix", name)
	}

	listen, backendURL, apiHost := cfg.Listen, cfg.BackendURL, cfg.APIHost
	var err error
	if listen == "" {
		serverListen := cfg.ServerListen
		if serverListen == "" {
			if cfg.ServerBaseURL == "" {
				return nil, fmt.Errorf(`app: could not initialize Handler for %q: "Listen", "ServerListen" and "ServerBaseURL" all undefined`, name)
			}
			parsedUrl, err := url.Parse(cfg.ServerBaseURL)
			if err != nil {
				return nil, fmt.Errorf("app: could not initialize Handler for %q: unparseable ServerBaseURL %q: %v", name, cfg.ServerBaseURL, err)
			}
			serverListen = parsedUrl.Host
		}
		listen, err = randListen(serverListen)
		if err != nil {
			return nil, err
		}
	}
	if backendURL == "" {
		if cfg.ServerBaseURL == "" {
			return nil, fmt.Errorf(`app: could not initialize Handler for %q: neither "BackendURL" or "ServerBaseURL" defined`, name)
		}
		backendURL, err = baseURL(cfg.ServerBaseURL, listen)
		if err != nil {
			return nil, err
		}
	}
	if apiHost == "" {
		if cfg.ServerBaseURL == "" {
			return nil, fmt.Errorf(`app: could not initialize Handler for %q: neither "APIHost" or "ServerBaseURL" defined`, name)
		}
		apiHost = cfg.ServerBaseURL + "/"
	}

	proxyURL, err := url.Parse(backendURL)
	if err != nil {
		return nil, fmt.Errorf("could not parse backendURL %q: %v", backendURL, err)
	}

	username, password := auth.RandToken(20), auth.RandToken(20)
	camliAuth := username + ":" + password
	basicAuth := auth.NewBasicAuth(username, password)
	envVars := map[string]string{
		"CAMLI_API_HOST":   apiHost,
		"CAMLI_AUTH":       camliAuth,
		"CAMLI_APP_LISTEN": listen,
	}
	var configURLPath string
	if cfg.AppConfig != nil {
		configURLPath = cfg.Prefix + "config.json"
		envVars["CAMLI_APP_CONFIG_URL"] = apiHost + strings.TrimPrefix(configURLPath, "/")
	}
	masterqueryURLPath := cfg.Prefix + "masterquery"
	envVars["CAMLI_APP_MASTERQUERY_URL"] = apiHost + strings.TrimPrefix(masterqueryURLPath, "/")

	return &Handler{
		name:               name,
		envVars:            envVars,
		auth:               basicAuth,
		appConfig:          cfg.AppConfig,
		prefix:             strings.TrimSuffix(cfg.Prefix, "/"),
		proxy:              httputil.NewSingleHostReverseProxy(proxyURL),
		backendURL:         backendURL,
		configURLPath:      configURLPath,
		masterqueryURLPath: masterqueryURLPath,
	}, nil
}

// InitHandler sets the app handler's search handler, if the app handler was configured
// to have one with HasSearch.
func (a *Handler) InitHandler(hl blobserver.FindHandlerByTyper) error {
	apName := a.ProgramName()
	searchPrefix, _, err := hl.FindHandlerByType("search")
	if err != nil {
		return fmt.Errorf("No search handler configured, which is necessary for the %v app handler", apName)
	}
	var sh *search.Handler
	_, hi := hl.AllHandlers()
	h, ok := hi[searchPrefix]
	if !ok {
		return fmt.Errorf("failed to find the \"search\" handler for %v", apName)
	}
	sh = h.(*search.Handler)
	a.sh = sh
	return nil
}

func (a *Handler) Start() error {
	name := a.name
	if name == "" {
		return fmt.Errorf("invalid app name: %q", name)
	}
	var binPath string
	var err error
	if e := os.Getenv("CAMLI_APP_BINDIR"); e != "" {
		binPath, err = exec.LookPath(filepath.Join(e, name))
		if err != nil {
			log.Printf("%q executable not found in %q", name, e)
		}
	}
	if binPath == "" || err != nil {
		binPath, err = exec.LookPath(name)
		if err != nil {
			return fmt.Errorf("%q executable not found in PATH", name)
		}
	}

	cmd := exec.Command(binPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// TODO(mpl): extract Env methods from dev/devcam/env.go to a util pkg and use them here.
	newVars := make(map[string]string, len(a.envVars))
	for k, v := range a.envVars {
		newVars[k+"="] = v
	}
	env := os.Environ()
	for pos, oldkv := range env {
		for k, newVal := range newVars {
			if strings.HasPrefix(oldkv, k) {
				env[pos] = k + newVal
				delete(newVars, k)
				break
			}
		}
	}
	for k, v := range newVars {
		env = append(env, k+v)
	}
	cmd.Env = env
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not start app %v: %v", name, err)
	}
	a.process = cmd.Process
	return nil
}

// ProgramName returns the name of the app's binary. It may be a file name in
// CAMLI_APP_BINDIR or PATH, or an absolute path.
func (a *Handler) ProgramName() string {
	return a.name
}

// AuthMode returns the app handler's auth mode, which is also the auth that the
// app's client will be configured with. This mode should be registered with
// the server's auth modes, for the app to have access to the server's resources.
func (a *Handler) AuthMode() auth.AuthMode {
	return a.auth
}

// AppConfig returns the optional configuration parameters object that the app
// can request from the app handler. It can be nil.
func (a *Handler) AppConfig() map[string]interface{} {
	return a.appConfig
}

// BackendURL returns the appBackendURL that the app handler will proxy to.
func (a *Handler) BackendURL() string {
	return a.backendURL
}

var errProcessTookTooLong = errors.New("process took too long to quit")

// Quit sends the app's process a SIGINT, and waits up to 5 seconds for it
// to exit, returning an error if it doesn't.
func (a *Handler) Quit() error {
	err := a.process.Signal(os.Interrupt)
	if err != nil {
		return err
	}

	c := make(chan error)
	go func() {
		_, err := a.process.Wait()
		c <- err
	}()
	select {
	case err = <-c:
	case <-time.After(5 * time.Second):
		// TODO Do we want to SIGKILL here or just leave the app alone?
		err = errProcessTookTooLong
	}
	return err
}
