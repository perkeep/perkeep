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
package app

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"camlistore.org/pkg/auth"
	camhttputil "camlistore.org/pkg/httputil"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/osutil"
)

// AppHandler acts as a reverse proxy for a server application started by
// Camlistore. It can also serve some extra JSON configuration to the app.
type AppHandler struct {
	name    string            // Name of the app's program.
	envVars map[string]string // Variables set in the app's process environment. See pkg/app/vars.txt.

	auth      auth.AuthMode  // Used for basic HTTP authenticating against the app requests.
	appConfig jsonconfig.Obj // Additional parameters the app can request, or nil.

	proxy *httputil.ReverseProxy // For redirecting requests to the app.
}

func (a *AppHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
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
	a.proxy.ServeHTTP(rw, req)
}

// New returns a configured AppHandler that Camlistore can use during server initialization
// as a handler that proxies request to an app. It is also used to start the app.
// The conf object has the following members, related to the vars described in doc/app-environment.text:
// "program", string, required. Name of the app's program.
// "baseURL", string, required. See CAMLI_APP_BASEURL.
// "server", string, optional, overrides the camliBaseURL argument. See CAMLI_SERVER.
// "appConfig", object, optional. Additional configuration that the app can request from Camlistore.
func New(conf jsonconfig.Obj, serverBaseURL string) (*AppHandler, error) {
	name := conf.RequiredString("program")
	server := conf.OptionalString("server", serverBaseURL)
	if server == "" {
		return nil, fmt.Errorf("could not initialize AppHandler for %q: Camlistore baseURL is unknown", name)
	}
	baseURL := conf.RequiredString("baseURL")
	appConfig := conf.OptionalObject("appConfig")
	// TODO(mpl): add an auth token in the extra config of the dev server config,
	// that the hello app can use to setup a status handler than only responds
	// to requests with that token.
	if err := conf.Validate(); err != nil {
		return nil, err
	}

	username, password := auth.RandToken(20), auth.RandToken(20)
	camliAuth := username + ":" + password
	basicAuth := auth.NewBasicAuth(username, password)
	envVars := map[string]string{
		"CAMLI_SERVER":      server,
		"CAMLI_AUTH":        camliAuth,
		"CAMLI_APP_BASEURL": baseURL,
	}
	if appConfig != nil {
		appConfigURL := fmt.Sprintf("%s/%s/%s", strings.TrimSuffix(server, "/"), name, "config.json")
		envVars["CAMLI_APP_CONFIG_URL"] = appConfigURL
	}

	proxyURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("could not parse baseURL %q: %v", baseURL, err)
	}
	return &AppHandler{
		name:      name,
		envVars:   envVars,
		auth:      basicAuth,
		appConfig: appConfig,
		proxy:     httputil.NewSingleHostReverseProxy(proxyURL),
	}, nil
}

func (a *AppHandler) Start() error {
	name := a.name
	if name == "" {
		return fmt.Errorf("invalid app name: %q", name)
	}
	// first look for it in PATH
	binPath, err := exec.LookPath(name)
	if err != nil {
		log.Printf("%q binary not found in PATH. now trying in the camlistore tree.", name)
		// else try in the camlistore tree
		binDir, err := osutil.GoPackagePath("camlistore.org/bin")
		if err != nil {
			return fmt.Errorf("bin dir in camlistore tree was not found: %v", err)
		}
		binPath = filepath.Join(binDir, name)
		if _, err = os.Stat(binPath); err != nil {
			return fmt.Errorf("could not find %v binary at %v: %v", name, binPath, err)
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
	return nil
}

func (a *AppHandler) Name() string {
	return a.name
}
