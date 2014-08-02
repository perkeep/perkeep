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

// Package app provides helpers for server applications interacting
// with Camlistore.
package app

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"camlistore.org/pkg/auth"
	"camlistore.org/pkg/client"
)

// Client returns a client from pkg/client, configured by environment variables
// for applications, and ready to be used to connect to the Camlistore server.
func Client() (*client.Client, error) {
	server := os.Getenv("CAMLI_API_HOST")
	if server == "" {
		return nil, errors.New("CAMLI_API_HOST var not set")
	}
	authString := os.Getenv("CAMLI_AUTH")
	if authString == "" {
		return nil, errors.New("CAMLI_AUTH var not set")
	}
	userpass := strings.Split(authString, ":")
	if len(userpass) != 2 {
		return nil, fmt.Errorf("invalid auth string syntax. got %q, want \"username:password\"", authString)
	}
	cl := client.NewFromParams(server, auth.NewBasicAuth(userpass[0], userpass[1]))
	cl.SetHTTPClient(&http.Client{
		Transport: cl.TransportForConfig(nil),
	})
	return cl, nil
}

// ListenAddress returns the host:[port] network address, derived from the environment,
// that the application should listen on.
func ListenAddress() (string, error) {
	// TODO(mpl): IPv6 support
	baseURL := os.Getenv("CAMLI_APP_BACKEND_URL")
	if baseURL == "" {
		return "", errors.New("CAMLI_APP_BACKEND_URL is undefined")
	}

	// TODO(mpl): see if can use netutil.TCPAddress (and get IP6 for free).
	defaultPort := "80"
	noScheme := strings.TrimPrefix(baseURL, "http://")
	if strings.HasPrefix(baseURL, "https://") {
		noScheme = strings.TrimPrefix(baseURL, "https://")
		defaultPort = "443"
	}
	hostPortPrefix := strings.SplitN(noScheme, "/", 2)
	if len(hostPortPrefix) != 2 {
		return "", fmt.Errorf("invalid CAMLI_APP_BACKEND_URL: %q (no trailing slash?)", baseURL)
	}
	if !strings.Contains(hostPortPrefix[0], ":") {
		return fmt.Sprintf("%s:%s", hostPortPrefix[0], defaultPort), nil
	}
	return hostPortPrefix[0], nil
}
