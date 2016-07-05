/*
Copyright 2014 The Camlistore Authors.

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
// from Camlistore.
// See also https://camlistore.org/doc/app-environment for the related
// variables.
package app // import "camlistore.org/pkg/server/app"

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"camlistore.org/pkg/auth"
	camhttputil "camlistore.org/pkg/httputil"
	"camlistore.org/pkg/netutil"

	"go4.org/jsonconfig"
)

// Handler acts as a reverse proxy for a server application started by
// Camlistore. It can also serve some extra JSON configuration to the app.
type Handler struct {
	name    string            // Name of the app's program.
	envVars map[string]string // Variables set in the app's process environment. See doc/app-environment.txt.

	auth      auth.AuthMode  // Used for basic HTTP authenticating against the app requests.
	appConfig jsonconfig.Obj // Additional parameters the app can request, or nil.

	// Prefix is the URL path prefix where the app handler is mounted on
	// Camlistore, stripped of its trailing slash. The handler trims this
	// prefix from incoming requests before proxying them to the app. Examples:
	// "/pics", "/blog".
	prefix     string
	proxy      *httputil.ReverseProxy // For redirecting requests to the app.
	backendURL string                 // URL that we proxy to (i.e. base URL of the app).

	process *os.Process // The app's Pid. To send it signals on restart, etc.
}

func (a *Handler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if camhttputil.PathSuffix(req) == "config.json" {
		if a.auth.AllowedAccess(req)&auth.OpGet == auth.OpGet {
			camhttputil.ReturnJSON(rw, a.appConfig)
		} else {
			auth.SendUnauthorized(rw, req)
		}
		return
	}
	if a.proxy == nil {
		http.Error(rw, "no proxy for the app", 500)
		return
	}
	req.URL.Path = strings.TrimPrefix(req.URL.Path, a.prefix)
	a.proxy.ServeHTTP(rw, req)
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

// baseURL concatenates the scheme and host parts of serverBaseURL with
// the port of listenAddr.
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
// https://camlistore.org/doc/app-environment for the corresponding environment
// variables.
type HandlerConfig struct {
	// Program is the file name of the server app's program executable. Either
	// an absolute path, or the name of a file located in CAMLI_APP_BINDIR or in PATH.
	Program string `json:"program"`

	// Prefix is the URL path prefix on APIHost where the app handler is mounted.
	// It always ends with a trailing slash. Examples: "/pics/", "/blog/".
	Prefix string `json:"prefix"`

	// Listen is the address (of the form host|ip:port) on which the app
	// will listen. It defines CAMLI_APP_LISTEN.
	// If empty, the default is the concatenation of ServerListen's host
	// part and a random port.
	Listen string `json:"listen,omitempty"`

	// ServerListen is the Camlistore server's listen address. Required if Listen is
	// not defined.
	ServerListen string `json:"serverListen,omitempty"`

	// BackendURL is the URL of the application's process, always ending in a
	// trailing slash. It is the URL that the app handler will proxy to when
	// getting requests for the concerned app.
	// If empty, the default is the concatenation of the ServerBaseURL
	// scheme, the ServerBaseURL host part, and the port of Listen.
	BackendURL string `json:"backendURL,omitempty"`

	// ServerBaseURL is the Camlistore server's BaseURL. Required if BackendURL is not
	// defined.
	ServerBaseURL string `json:"serverBaseURL,omitempty"`

	// APIHost is the URL of the Camlistore server which the app should
	// use to make API calls. It always ends in a trailing slash. It defines CAMLI_API_HOST.
	// If empty, the default is ServerBaseURL, with a trailing slash appended.
	APIHost string `json:"apiHost,omitempty"`

	// AppConfig contains some additional configuration specific to each app.
	// See CAMLI_APP_CONFIG_URL.
	AppConfig jsonconfig.Obj
}

// FromJSONConfig creates an HandlerConfig from the contents of config.
// serverBaseURL is used if it is not found in config.
func FromJSONConfig(config jsonconfig.Obj, serverBaseURL string) (HandlerConfig, error) {
	hc := HandlerConfig{
		Program:       config.RequiredString("program"),
		Prefix:        config.RequiredString("prefix"),
		BackendURL:    config.OptionalString("backendURL", ""),
		Listen:        config.OptionalString("listen", ""),
		APIHost:       config.OptionalString("apiHost", ""),
		ServerListen:  config.OptionalString("serverListen", ""),
		ServerBaseURL: config.OptionalString("serverBaseURL", ""),
		AppConfig:     config.OptionalObject("appConfig"),
	}
	if hc.ServerBaseURL == "" {
		hc.ServerBaseURL = serverBaseURL
	}
	if err := config.Validate(); err != nil {
		return HandlerConfig{}, err
	}
	return hc, nil
}

func NewHandler(cfg HandlerConfig) (*Handler, error) {
	name := cfg.Program
	if cfg.Prefix == "" {
		return nil, fmt.Errorf("app: could not initialize Handler for %q: empty Prefix", name)
	}

	listen, backendURL, apiHost := cfg.Listen, cfg.BackendURL, cfg.APIHost
	var err error
	if listen == "" {
		if cfg.ServerListen == "" {
			return nil, fmt.Errorf(`app: could not initialize Handler for %q: neither "Listen" or "ServerListen" defined`, name)
		}
		listen, err = randListen(cfg.ServerListen)
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
	if cfg.AppConfig != nil {
		envVars["CAMLI_APP_CONFIG_URL"] = apiHost + strings.TrimPrefix(cfg.Prefix, "/") + "config.json"
	}

	return &Handler{
		name:       name,
		envVars:    envVars,
		auth:       basicAuth,
		appConfig:  cfg.AppConfig,
		prefix:     strings.TrimSuffix(cfg.Prefix, "/"),
		proxy:      httputil.NewSingleHostReverseProxy(proxyURL),
		backendURL: backendURL,
	}, nil
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
			return fmt.Errorf("%q executable not found in PATH.", name)
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

var errProcessTookTooLong = errors.New("proccess took too long to quit")

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
