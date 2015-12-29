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
	"os"
	"strings"

	"camlistore.org/pkg/auth"
	"camlistore.org/pkg/client"
	"camlistore.org/pkg/netutil"
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
	return cl, nil
}

// ListenAddress returns the host:[port] network address, derived from the environment,
// that the application should listen on.
func ListenAddress() (string, error) {
	baseURL := os.Getenv("CAMLI_APP_BACKEND_URL")
	if baseURL == "" {
		return "", errors.New("CAMLI_APP_BACKEND_URL is undefined")
	}
	return netutil.HostPort(baseURL)
}
