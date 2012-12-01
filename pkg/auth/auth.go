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
	"net"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"strings"

	"camlistore.org/pkg/netutil"
)

var kBasicAuthPattern *regexp.Regexp = regexp.MustCompile(`^Basic ([a-zA-Z0-9\+/=]+)`)

var (
	mode AuthMode // the auth logic depending on the choosen auth mechanism
)

type AuthMode interface {
	// IsAuthorized checks the credentials in req.
	IsAuthorized(req *http.Request) bool
	// AddAuthHeader inserts in req the credentials needed
	// for a client to authenticate.
	AddAuthHeader(req *http.Request)
}

func FromEnv() (AuthMode, error) {
	return FromConfig(os.Getenv("CAMLI_AUTH"))
}

// FromConfig parses authConfig and accordingly sets up the AuthMode
// that will be used for all upcoming authentication exchanges. The
// supported modes are UserPass and DevAuth. UserPass requires an authConfig
// of the kind "userpass:joe:ponies". If the CAMLI_ADVERTISED_PASSWORD
// environment variable is defined, the mode will default to DevAuth.
func FromConfig(authConfig string) (AuthMode, error) {
	pieces := strings.Split(authConfig, ":")
	if len(pieces) < 1 {
		return nil, fmt.Errorf("Invalid auth string: %q", authConfig)
	}
	authType := pieces[0]

	if pw := os.Getenv("CAMLI_ADVERTISED_PASSWORD"); pw != "" {
		mode = &DevAuth{pw}
		return mode, nil
	}

	switch authType {
	case "none":
		mode = None{}
	case "localhost":
		mode = Localhost{}
	case "userpass":
		if len(pieces) < 3 {
			return nil, fmt.Errorf("Wrong userpass auth string; needs to be \"userpass:user:password\"")
		}
		username := pieces[1]
		password := pieces[2]
		mode = &UserPass{Username: username, Password: password}
		for _, opt := range pieces[3:] {
			switch opt {
			case "+localhost":
				mode.(*UserPass).OrLocalhost = true
			default:
				return nil, fmt.Errorf("Unknown userpass option %q", opt)
			}
		}
	default:
		return nil, fmt.Errorf("Unknown auth type: %q", authType)
	}
	return mode, nil
}

func basicAuth(req *http.Request) (string, string, error) {
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
	OrLocalhost        bool // if true, allow localhost ident auth too
}

func (up *UserPass) IsAuthorized(req *http.Request) bool {
	if up.OrLocalhost && localhostAuthorized(req) {
		return true
	}
	user, pass, err := basicAuth(req)
	if err != nil {
		return false
	}
	return user == up.Username && pass == up.Password
}

func (up *UserPass) AddAuthHeader(req *http.Request) {
	req.SetBasicAuth(up.Username, up.Password)
}

type None struct{}

func (None) IsAuthorized(req *http.Request) bool {
	return true
}

type Localhost struct {
	None
}

func (Localhost) IsAuthorized(req *http.Request) bool {
	return localhostAuthorized(req)
}

func (None) AddAuthHeader(req *http.Request) {
	// Nothing.
}

// DevAuth is used when the env var CAMLI_ADVERTISED_PASSWORD
// is defined
type DevAuth struct {
	Password string
}

func localhostAuthorized(req *http.Request) bool {
	uid := os.Getuid()
	from, err := netutil.HostPortToIP(req.RemoteAddr)
	if err != nil {
		return false
	}
	to, err := netutil.HostPortToIP(req.Host)
	if err != nil {
		return false
	}

	// If our OS doesn't support uid.
	// TODO(bradfitz): netutil on OS X uses "lsof" to figure out
	// ownership of tcp connections, but when fuse is mounted and a
	// request is outstanding (for instance, a fuse request that's
	// making a request to camlistored and landing in this code
	// path), lsof then blocks forever waiting on a lock held by the
	// VFS, leading to a deadlock.  Instead, on darwin, just trust
	// any localhost connection here, which is kinda lame, but
	// whatever.  Macs aren't very multi-user anyway.
	if uid == -1 || runtime.GOOS == "darwin" {
		return from.IP.IsLoopback() && to.IP.IsLoopback()
	}

	if uid > 0 {
		owner, err := netutil.AddrPairUserid(from, to)
		if err == nil && owner == uid {
			return true
		}
	}
	return false
}

func isLocalhost(addrPort net.IP) bool {
	return addrPort.IsLoopback()
}

func LocalhostAuthorized(req *http.Request) bool {
	return localhostAuthorized(req)
}

func (da *DevAuth) IsAuthorized(req *http.Request) bool {
	// First see if the local TCP port is owned by the same
	// non-root user as this server.
	if localhostAuthorized(req) {
		return true
	}

	_, pass, err := basicAuth(req)
	if err != nil {
		return false
	}
	return pass == da.Password
}

func (da *DevAuth) AddAuthHeader(req *http.Request) {
	req.SetBasicAuth("", da.Password)
}

func IsAuthorized(req *http.Request) bool {
	return mode.IsAuthorized(req)
}

func TriedAuthorization(req *http.Request) bool {
	// Currently a simple test just using HTTP basic auth
	// (presumably over https); may expand.
	return req.Header.Get("Authorization") != ""
}

func SendUnauthorized(conn http.ResponseWriter) {
	realm := "camlistored"
	if devAuth, ok := mode.(*DevAuth); ok {
		realm = "Any username, password is: " + devAuth.Password
	}
	conn.Header().Set("WWW-Authenticate", fmt.Sprintf("Basic realm=%q", realm))
	conn.WriteHeader(http.StatusUnauthorized)
	fmt.Fprintf(conn, "<h1>Unauthorized</h1>")
}

type Handler struct {
	http.Handler
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if mode.IsAuthorized(r) {
		h.Handler.ServeHTTP(w, r)
	} else {
		SendUnauthorized(w)
	}
}

// requireAuth wraps a function with another function that enforces
// HTTP Basic Auth.
func RequireAuth(handler func(conn http.ResponseWriter, req *http.Request)) func(conn http.ResponseWriter, req *http.Request) {
	return func(conn http.ResponseWriter, req *http.Request) {
		if mode.IsAuthorized(req) {
			handler(conn, req)
		} else {
			SendUnauthorized(conn)
		}
	}
}
