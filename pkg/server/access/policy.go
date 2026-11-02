/*
Copyright 2026 The Perkeep Authors

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

package access

import (
	"fmt"
	"net/http"
	"path"
	"strings"
)

// Perm is a set of permissions an access key can have.
type Perm uint8

const (
	PermUpload   Perm = 1 << iota // upload blobs, and stat them
	PermSign                      // have the server sign claims and other schema blobs
	PermSearch                    // run searches and describe blobs
	PermDownload                  // fetch and enumerate blobs, and files' contents and thumbnails
	PermStats                     // get the summary of the server's storage in discovery
)

// permInfo describes each permission, in the order the Access page lists
// them.
//
// The names are the permissions' format in the state file, so they must
// never change, or the state files that use them won't load; see
// TestPermNames. (The Perm bits aren't saved, so they may.)
var permInfo = []struct {
	perm        Perm
	name        string // in the state file and forms; never change one
	title, desc string
}{
	{PermUpload, "upload", "Upload blobs", "Upload blobs, and check which ones the server has, and which files' contents have permanodes."},
	{PermSign, "sign", "Make claims", "Have the server sign claims and permanodes with its identity, without the client holding the GPG private key."},
	{PermSearch, "search", "Search", "Run searches and describe blobs."},
	{PermDownload, "download", "Download blobs", "Fetch and enumerate blobs, and download files and their thumbnails."},
	{PermStats, "stats", "Server stats", "See how many blobs the server has, and how much space they take."},
}

func (p Perm) Has(q Perm) bool { return p&q == q }

// Names returns the names of the permissions in p.
func (p Perm) Names() []string {
	var names []string
	for _, pi := range permInfo {
		if p.Has(pi.perm) {
			names = append(names, pi.name)
		}
	}
	return names
}

// ParsePerms returns the permissions with the given names.
func ParsePerms(names []string) (Perm, error) {
	var p Perm
	for _, name := range names {
		found := false
		for _, pi := range permInfo {
			if pi.name == name {
				p |= pi.perm
				found = true
			}
		}
		if !found {
			return 0, fmt.Errorf("unknown permission %q", name)
		}
	}
	return p, nil
}

// Roots are the URL prefixes, like "/bs/", of the handlers that the
// tailcat listener serves parts of to access keys. Empty ones aren't
// served.
type Roots struct {
	Blob   string // blob storage, as in discovery's blobRoot
	Search string
	Sign   string // jsonsign
	UI     string
}

// searchGets and searchPosts are the search handler's endpoints, under
// $searchRoot/camli/search/, that PermSearch allows. They're all read-only;
// its WebSocket isn't one of them.
var (
	searchGets = map[string]bool{
		"recent":          true,
		"permanodeattr":   true,
		"describe":        true,
		"exif":            true,
		"claims":          true,
		"files":           true,
		"signerattrvalue": true,
		"signerpaths":     true,
		"edgesto":         true,
	}
	searchPosts = map[string]bool{
		"describe": true,
		"query":    true,
	}
)

// need returns the permissions of which an access key needs at least one
// for r, or zero if any valid key may make r. It reports ok=false for
// requests that the tailcat listener never serves, which is anything not
// explicitly listed here.
func (rt Roots) need(r *http.Request) (anyOf Perm, ok bool) {
	p := r.URL.Path
	if !strings.HasPrefix(p, "/") || (p != path.Clean(p) && p != path.Clean(p)+"/") {
		// Let nothing reach the handlers under a different name
		// than the one checked here.
		return 0, false
	}
	isGet := r.Method == "GET" || r.Method == "HEAD"
	isPost := r.Method == "POST"
	under := func(root string) (suffix string, ok bool) {
		if root == "" {
			return "", false
		}
		return strings.CutPrefix(p, root)
	}

	if p == "/" {
		if isGet && wantsDiscovery(r) {
			return 0, true
		}
		return 0, false
	}
	if action, ok := under(rt.Blob + "camli/"); ok {
		switch {
		case action == "session" || strings.HasPrefix(action, "session/"):
			if isPost || r.Method == "DELETE" {
				return PermUpload, true
			}
		case action == "stat":
			if isGet || isPost {
				return PermUpload | PermDownload, true
			}
		case action == "upload":
			if isPost {
				return PermUpload, true
			}
		case action == "enumerate-blobs":
			if isGet {
				return PermDownload, true
			}
		case action == "ws" || action == "remove" || action == "" || strings.Contains(action, "/"):
		default:
			// A blobref.
			switch {
			case isGet:
				return PermDownload, true
			case r.Method == "PUT":
				return PermUpload, true
			}
		}
		return 0, false
	}
	if suffix, ok := under(rt.Search + "camli/search/"); ok {
		if suffix == "contentpermanodes" && (isGet || isPost) {
			// Uploaders need to know which of their files the server
			// has as permanodes, to make permanodes only for the others.
			return PermUpload | PermSign | PermSearch, true
		}
		if (isGet && searchGets[suffix]) || (isPost && searchPosts[suffix]) {
			return PermSearch, true
		}
		return 0, false
	}
	if suffix, ok := under(rt.Sign); ok {
		// GETs are for its discovery and public key.
		if isGet || (isPost && (suffix == "camli/sig/sign" || suffix == "camli/sig/verify")) {
			return PermSign, true
		}
		return 0, false
	}
	if suffix, ok := under(rt.UI); ok {
		if isGet && (strings.HasPrefix(suffix, "download/") || strings.HasPrefix(suffix, "thumbnail/")) && !wantsDiscovery(r) {
			return PermDownload, true
		}
		return 0, false
	}
	return 0, false
}

// wantsDiscovery reports whether r asks for the server's discovery JSON,
// as perkeep.org/pkg/server's root handler decides.
func wantsDiscovery(r *http.Request) bool {
	return r.Header.Get("Accept") == "text/x-camli-configuration" ||
		r.URL.Query().Get("camli.mode") == "config"
}
