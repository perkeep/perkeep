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

// Package app provides helpers for server applications interacting
// with Perkeep.
// See also https://perkeep.org/doc/app-environment for the related
// variables.
package app // import "perkeep.org/pkg/app"

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/client"
)

// Auth returns the auth mode for the app to access Perkeep, as defined by
// environment variables automatically supplied by the Perkeep server host.
func Auth() (auth.AuthMode, error) {
	return basicAuth()
}

func basicAuth() (auth.AuthMode, error) {
	authString := os.Getenv("CAMLI_AUTH")
	if authString == "" {
		return nil, errors.New("CAMLI_AUTH var not set")
	}
	userpass := strings.Split(authString, ":")
	if len(userpass) != 2 {
		return nil, fmt.Errorf("invalid auth string syntax. got %q, want \"username:password\"", authString)
	}
	return auth.NewBasicAuth(userpass[0], userpass[1]), nil
}

// Client returns a Perkeep client as defined by environment variables
// automatically supplied by the Perkeep server host.
func Client() (*client.Client, error) {
	server := os.Getenv("CAMLI_API_HOST")
	if server == "" {
		return nil, errors.New("CAMLI_API_HOST var not set")
	}
	am, err := basicAuth()
	if err != nil {
		return nil, err
	}
	return client.New(
		client.OptionNoExternalConfig(),
		client.OptionServer(server),
		client.OptionAuthMode(am),
	)
}

// ListenAddress returns the host:[port] network address, derived from the environment,
// that the application should listen on.
func ListenAddress() (string, error) {
	listenAddr := os.Getenv("CAMLI_APP_LISTEN")
	if listenAddr == "" {
		return "", errors.New("CAMLI_APP_LISTEN is undefined")
	}
	return listenAddr, nil
}

// PathPrefix returns the app's prefix on the app handler if the request was proxied
// through Perkeep, or "/" if the request went directly to the app.
func PathPrefix(r *http.Request) string {
	if prefix := httputil.PathBase(r); prefix != "" {
		return prefix
	}
	return "/"
}
