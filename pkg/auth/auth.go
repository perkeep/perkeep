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

// Package auth implements Camlistore authentication.
package auth

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"

	"camlistore.org/pkg/httputil"
)

// Operation represents a bitmask of operations. See the OpX constants.
type Operation int

const (
	OpUpload Operation = 1 << iota
	OpStat
	OpGet
	OpEnumerate
	OpRemove
	OpSign
	OpDiscovery
	OpRead   = OpEnumerate | OpStat | OpGet | OpDiscovery
	OpRW     = OpUpload | OpEnumerate | OpStat | OpGet // Not Remove
	OpVivify = OpUpload | OpStat | OpGet | OpDiscovery
	OpAll    = OpUpload | OpEnumerate | OpStat | OpRemove | OpGet | OpSign | OpDiscovery
)

var (
	// Each mode defines an auth logic which depends on the choosen auth mechanism.
	// Access is allowed if any of the modes allows it.
	// No need to guard for now as all the writes are done sequentially during setup.
	modes []AuthMode
)

// An AuthMode is the interface implemented by diffent authentication
// schemes.
type AuthMode interface {
	// AllowedAccess returns a bitmask of all operations
	// this user/request is allowed to do.
	AllowedAccess(req *http.Request) Operation
	// AddAuthHeader inserts in req the credentials needed
	// for a client to authenticate.
	AddAuthHeader(req *http.Request)
}

// UnauthorizedSender may be implemented by AuthModes which want to
// handle sending unauthorized.
type UnauthorizedSender interface {
	// SendUnauthorized sends an unauthorized response,
	// and returns whether it handled it.
	SendUnauthorized(http.ResponseWriter, *http.Request) (handled bool)
}

func FromEnv() (AuthMode, error) {
	return FromConfig(os.Getenv("CAMLI_AUTH"))
}

// An AuthConfigParser parses a registered authentication type's option
// and returns an AuthMode.
type AuthConfigParser func(arg string) (AuthMode, error)

var authConstructor = map[string]AuthConfigParser{
	"none":      newNoneAuth,
	"localhost": newLocalhostAuth,
	"userpass":  newUserPassAuth,
	"devauth":   newDevAuth,
	"basic":     newBasicAuth,
}

// RegisterAuth registers a new authentication scheme.
func RegisterAuth(name string, ctor AuthConfigParser) {
	if _, dup := authConstructor[name]; dup {
		panic("Dup registration of auth mode " + name)
	}
	authConstructor[name] = ctor
}

func newNoneAuth(string) (AuthMode, error) {
	return None{}, nil
}

func newLocalhostAuth(string) (AuthMode, error) {
	return Localhost{}, nil
}

func newDevAuth(pw string) (AuthMode, error) {
	// the vivify mode password is automatically set to "vivi" + Password
	vp := "vivi" + pw
	return &DevAuth{
		Password:   pw,
		VivifyPass: &vp,
	}, nil
}

func newUserPassAuth(arg string) (AuthMode, error) {
	pieces := strings.Split(arg, ":")
	if len(pieces) < 2 {
		return nil, fmt.Errorf("Wrong userpass auth string; needs to be \"user:password\"")
	}
	username := pieces[0]
	password := pieces[1]
	mode := &UserPass{Username: username, Password: password}
	for _, opt := range pieces[2:] {
		switch {
		case opt == "+localhost":
			mode.OrLocalhost = true
		case strings.HasPrefix(opt, "vivify="):
			// optional vivify mode password: "userpass:joe:ponies:vivify=rainbowdash"
			vp := strings.Replace(opt, "vivify=", "", -1)
			mode.VivifyPass = &vp
		default:
			return nil, fmt.Errorf("Unknown userpass option %q", opt)
		}
	}
	return mode, nil
}

func newBasicAuth(arg string) (AuthMode, error) {
	pieces := strings.Split(arg, ":")
	if len(pieces) != 2 {
		return nil, fmt.Errorf("invalid basic auth syntax. got %q, want \"username:password\"", arg)
	}
	return NewBasicAuth(pieces[0], pieces[1]), nil
}

// NewBasicAuth returns a UserPass Authmode, adequate to support HTTP
// basic authentication.
func NewBasicAuth(username, password string) AuthMode {
	return &UserPass{
		Username: username,
		Password: password,
	}
}

// ErrNoAuth is returned when there is no configured authentication.
var ErrNoAuth = errors.New("auth: no configured authentication")

// FromConfig parses authConfig and accordingly sets up the AuthMode
// that will be used for all upcoming authentication exchanges. The
// supported modes are UserPass and DevAuth. UserPass requires an authConfig
// of the kind "userpass:joe:ponies".
//
// If the input string is empty, the error will be ErrNoAuth.
func FromConfig(authConfig string) (AuthMode, error) {
	if authConfig == "" {
		return nil, ErrNoAuth
	}
	pieces := strings.SplitN(authConfig, ":", 2)
	if len(pieces) < 1 {
		return nil, fmt.Errorf("Invalid auth string: %q", authConfig)
	}
	authType := pieces[0]

	if fn, ok := authConstructor[authType]; ok {
		arg := ""
		if len(pieces) == 2 {
			arg = pieces[1]
		}
		return fn(arg)
	}
	return nil, fmt.Errorf("Unknown auth type: %q", authType)
}

// SetMode sets the given authentication mode as the only allowed one for
// future requests. That is, it replaces all modes that were previously added.
func SetMode(m AuthMode) {
	modes = []AuthMode{m}
}

// AddMode adds the given authentication mode to the list of modes that
// future requests can authenticate against.
func AddMode(am AuthMode) {
	modes = append(modes, am)
}

// UserPass is used when the auth string provided in the config
// is of the kind "userpass:username:pass"
// Possible options appended to the config string are
// "+localhost" and "vivify=pass", where pass will be the
// alternative password which only allows the vivify operation.
type UserPass struct {
	Username, Password string
	OrLocalhost        bool // if true, allow localhost ident auth too

	// VivifyPass, if not nil, is the alternative password used (only) for the vivify operation.
	// It is checked when uploading, but Password takes precedence.
	VivifyPass *string
}

func (up *UserPass) AllowedAccess(req *http.Request) Operation {
	user, pass, err := httputil.BasicAuth(req)
	if err == nil {
		if user == up.Username {
			if pass == up.Password {
				return OpAll
			}
			if up.VivifyPass != nil && pass == *up.VivifyPass {
				return OpVivify
			}
		}
	}

	if websocketTokenMatches(req) {
		return OpAll
	}
	if up.OrLocalhost && httputil.IsLocalhost(req) {
		return OpAll
	}

	return 0
}

func (up *UserPass) AddAuthHeader(req *http.Request) {
	req.SetBasicAuth(up.Username, up.Password)
}

type None struct{}

func (None) AllowedAccess(req *http.Request) Operation {
	return OpAll
}

func (None) AddAuthHeader(req *http.Request) {
	// Nothing.
}

type Localhost struct {
	None
}

func (Localhost) AllowedAccess(req *http.Request) (out Operation) {
	if httputil.IsLocalhost(req) {
		return OpAll
	}
	return 0
}

// DevAuth is used for development.  It has one password and one vivify password, but
// also accepts all passwords from localhost. Usernames are ignored.
type DevAuth struct {
	Password string
	// Password for the vivify mode, automatically set to "vivi" + Password
	VivifyPass *string
}

func (da *DevAuth) AllowedAccess(req *http.Request) Operation {
	_, pass, err := httputil.BasicAuth(req)
	if err == nil {
		if pass == da.Password {
			return OpAll
		}
		if da.VivifyPass != nil && pass == *da.VivifyPass {
			return OpVivify
		}
	}

	if websocketTokenMatches(req) {
		return OpAll
	}

	// See if the local TCP port is owned by the same non-root user as this
	// server.  This check performed last as it may require reading from the
	// kernel or exec'ing a program.
	if httputil.IsLocalhost(req) {
		return OpAll
	}

	return 0
}

func (da *DevAuth) AddAuthHeader(req *http.Request) {
	req.SetBasicAuth("", da.Password)
}

func IsLocalhost(req *http.Request) bool {
	return httputil.IsLocalhost(req)
}

// AllowedWithAuth returns whether the given request
// has access to perform all the operations in op
// against am.
func AllowedWithAuth(am AuthMode, req *http.Request, op Operation) bool {
	if op&OpUpload != 0 {
		// upload (at least from camput) requires stat and get too
		op = op | OpVivify
	}
	return am.AllowedAccess(req)&op == op
}

// Allowed returns whether the given request
// has access to perform all the operations in op.
func Allowed(req *http.Request, op Operation) bool {
	for _, m := range modes {
		if AllowedWithAuth(m, req, op) {
			return true
		}
	}
	return false
}

func websocketTokenMatches(req *http.Request) bool {
	return req.Method == "GET" &&
		req.Header.Get("Upgrade") == "websocket" &&
		req.FormValue("authtoken") == ProcessRandom()
}

func TriedAuthorization(req *http.Request) bool {
	// Currently a simple test just using HTTP basic auth
	// (presumably over https); may expand.
	return req.Header.Get("Authorization") != ""
}

func SendUnauthorized(rw http.ResponseWriter, req *http.Request) {
	for _, m := range modes {
		if us, ok := m.(UnauthorizedSender); ok {
			if us.SendUnauthorized(rw, req) {
				return
			}
		}
	}
	var realm string
	hasDevAuth := func() (*DevAuth, bool) {
		for _, m := range modes {
			if devAuth, ok := m.(*DevAuth); ok {
				return devAuth, ok
			}
		}
		return nil, false
	}
	if devAuth, ok := hasDevAuth(); ok {
		realm = "Any username, password is: " + devAuth.Password
	}
	// From what I've tested, it looks like sending just "Basic" would be ok,
	// but RFC 2617 says realm is mandatory, so probably better to send an empty one.
	rw.Header().Set("WWW-Authenticate", fmt.Sprintf("Basic realm=%q", realm))
	rw.WriteHeader(http.StatusUnauthorized)
	fmt.Fprintf(rw, "<html><body><h1>Unauthorized</h1>")
}

type Handler struct {
	http.Handler
}

// ServeHTTP serves only if this request and auth mode are allowed all Operations.
func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.serveHTTPForOp(w, r, OpAll)
}

// serveHTTPForOp serves only if op is allowed for this request and auth mode.
func (h Handler) serveHTTPForOp(w http.ResponseWriter, r *http.Request, op Operation) {
	if Allowed(r, op) {
		h.Handler.ServeHTTP(w, r)
	} else {
		SendUnauthorized(w, r)
	}
}

// RequireAuth wraps a function with another function that enforces
// HTTP Basic Auth and checks if the operations in op are all permitted.
func RequireAuth(h http.Handler, op Operation) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if Allowed(req, op) {
			h.ServeHTTP(rw, req)
		} else {
			SendUnauthorized(rw, req)
		}
	})
}

var (
	processRand     string
	processRandOnce sync.Once
)

func ProcessRandom() string {
	processRandOnce.Do(genProcessRand)
	return processRand
}

func genProcessRand() {
	processRand = RandToken(20)
}

// RandToken genererates (with crypto/rand.Read) and returns a token
// that is the hex version (2x size) of size bytes of randomness.
func RandToken(size int) string {
	buf := make([]byte, size)
	if n, err := rand.Read(buf); err != nil || n != len(buf) {
		panic("failed to get random: " + err.Error())
	}
	return fmt.Sprintf("%x", buf)
}
