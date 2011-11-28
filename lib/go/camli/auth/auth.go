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

package auth

import (
	"encoding/base64"
	"fmt"
	"http"
	"os"
	"regexp"
	"strings"
)

var kBasicAuthPattern *regexp.Regexp = regexp.MustCompile(`^Basic ([a-zA-Z0-9\+/=]+)`)

var (
	Type string   // how to do auth. currently supported: "userpass"
	mode AuthMode // the auth logic corresponding to Type
)

type AuthMode interface {
	isAuthorized(req *http.Request) bool
}

func FromEnv() (AuthMode, os.Error) {
	return FromConfig(os.Getenv("CAMLI_AUTH"))
}

func FromConfig(authConfig string) (AuthMode, os.Error) {
	pieces := strings.Split(authConfig, ":")
	if len(pieces) < 1 {
		return nil, fmt.Errorf("Invalid auth string: %q", authConfig)
	}
	Type = pieces[0]
	switch Type {
	case "userpass":
		if len(pieces) != 3 {
			return nil, fmt.Errorf("Wrong userpass auth string; needs to be \"userpass:user:password\"")
		}
		mode = &UserPass{pieces[1], pieces[2]}
	case "":
		password := os.Getenv("CAMLI_ADVERTISED_PASSWORD")
		if password != "" {
			mode = &DevAuth{password}
		} else {
			return nil, fmt.Errorf("No auth string provided and no \"CAMLI_ADVERTISED_PASSWORD\" defined")
		}
	default:
		return nil, fmt.Errorf("Unknown auth type: %q", Type)
	}
	return mode, nil
}

func basicAuth(req *http.Request) (string, string, os.Error) {
	auth := req.Header.Get("Authorization")
	if auth == "" {
		return "", "", fmt.Errorf("Missing \"Authorization\" in header")
	}
	matches := kBasicAuthPattern.FindStringSubmatch(auth)
	if len(matches) != 2 {
		return "", "", fmt.Errorf("Bogus Authorization header")
	}
	encoded := matches[1]
	enc := base64.StdEncoding
	decBuf := make([]byte, enc.DecodedLen(len(encoded)))
	n, err := enc.Decode(decBuf, []byte(encoded))
	if err != nil {
		return "", "", err
	}
	pieces := strings.SplitN(string(decBuf[0:n]), ":", 2)
	if len(pieces) != 2 {
		return "", "", fmt.Errorf("didn't get two pieces")
	}
	return pieces[0], pieces[1], nil
}

// UserPass is used when the auth string provided in the config
// is of the kind "userpass:username:pass"
type UserPass struct {
	Username, Password string
}

func (up *UserPass) isAuthorized(req *http.Request) bool {
	user, pass, err := basicAuth(req)
	if err != nil {
		fmt.Printf("basic auth: %q", err)
		return false
	}
	return user == up.Username && pass == up.Password
}

// DevAuth is used when no auth string is provided in the config 
// and the env var CAMLI_ADVERTISED_PASSWORD is defined
type DevAuth struct {
	Password string
}

func (da *DevAuth) isAuthorized(req *http.Request) bool {
	_, pass, err := basicAuth(req)
	if err != nil {
		fmt.Printf("basic auth: %q", err)
		return false
	}
	return pass == da.Password
}

func IsAuthorized(req *http.Request) bool {
	return mode.isAuthorized(req)
}

func TriedAuthorization(req *http.Request) bool {
	// Currently a simple test just using HTTP basic auth
	// (presumably over https); may expand.
	return req.Header.Get("Authorization") != ""
}

func SendUnauthorized(conn http.ResponseWriter) {
	realm := "camlistored"
	if Type == "dev" {
		realm = "Any username, password is: " + mode.(*DevAuth).Password
	}
	conn.Header().Set("WWW-Authenticate", fmt.Sprintf("Basic realm=%q", realm))
	conn.WriteHeader(http.StatusUnauthorized)
	fmt.Fprintf(conn, "<h1>Unauthorized</h1>")
}

// requireAuth wraps a function with another function that enforces
// HTTP Basic Auth.
func RequireAuth(handler func(conn http.ResponseWriter, req *http.Request)) func(conn http.ResponseWriter, req *http.Request) {
	return func(conn http.ResponseWriter, req *http.Request) {
		if mode.isAuthorized(req) {
			handler(conn, req)
		} else {
			SendUnauthorized(conn)
		}
	}
}
