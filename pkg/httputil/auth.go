/*
Copyright 2013 The Camlistore Authors

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

package httputil

import (
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"strings"

	"camlistore.org/pkg/netutil"
)

var kBasicAuthPattern = regexp.MustCompile(`^Basic ([a-zA-Z0-9\+/=]+)`)

// IsLocalhost reports whether the requesting connection is from this machine
// and has the same owner as this process.
func IsLocalhost(req *http.Request) bool {
	uid := os.Getuid()
	from, err := netutil.HostPortToIP(req.RemoteAddr, nil)
	if err != nil {
		return false
	}
	to, err := netutil.HostPortToIP(req.Host, from)
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
	if uid == 0 {
		log.Printf("camlistored running as root. Don't do that.")
		return false
	}
	if uid > 0 {
		connUid, err := netutil.AddrPairUserid(from, to)
		if err == nil {
			if uid == connUid || connUid == 0 {
				// If it's the same user who's running the server, allow it.
				// Also allow root, so users can "sudo camput" files.
				// Allowing root isn't a security problem because if root wants
				// to mess with the local user, they already can. This whole mechanism
				// is about protecting regular users from other regular users
				// on shared computers.
				return true
			}
			log.Printf("auth: local connection uid %d doesn't match server uid %d", connUid, uid)
		}
	}
	return false
}

// BasicAuth parses the Authorization header on req
// If absent or invalid, an error is returned.
func BasicAuth(req *http.Request) (username, password string, err error) {
	auth := req.Header.Get("Authorization")
	if auth == "" {
		err = fmt.Errorf("Missing \"Authorization\" in header")
		return
	}
	matches := kBasicAuthPattern.FindStringSubmatch(auth)
	if len(matches) != 2 {
		err = fmt.Errorf("Bogus Authorization header")
		return
	}
	encoded := matches[1]
	enc := base64.StdEncoding
	decBuf := make([]byte, enc.DecodedLen(len(encoded)))
	n, err := enc.Decode(decBuf, []byte(encoded))
	if err != nil {
		return
	}
	pieces := strings.SplitN(string(decBuf[0:n]), ":", 2)
	if len(pieces) != 2 {
		err = fmt.Errorf("didn't get two pieces")
		return
	}
	return pieces[0], pieces[1], nil
}
