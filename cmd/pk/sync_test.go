/*
Copyright 2013 The Perkeep Authors

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

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"perkeep.org/pkg/client"
	"perkeep.org/pkg/cmdmain"
	"perkeep.org/pkg/types/camtypes"
)

const (
	syncTestKeyID       = "0123456789ABCDEF"
	syncTestOtherKeyID  = "FEDCBA9876543210"
	syncTestFingerprint = "AAAAAAAAAAAAAAAAAAAAAAAA0123456789ABCDEF"
	// Deliberately share the long key ID, but not the full fingerprint.
	syncTestCollisionFingerprint = "BBBBBBBBBBBBBBBBBBBBBBBB0123456789ABCDEF"
	syncTestOtherFingerprint     = "CCCCCCCCCCCCCCCCCCCCCCCCFEDCBA9876543210"
)

func syncTestConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CAMLI_CONFIG_DIR", dir)
	t.Setenv("CAMLI_AUTH", "none")
	if err := os.WriteFile(filepath.Join(dir, "client-config.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
}

// The empty enumeration lets RunCommand exercise discovery and both warnings
// without transferring any blobs.
func syncTestServer(t *testing.T, signing *camtypes.SignDiscovery) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var discoveries atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			discoveries.Add(1)
			w.Header().Set("Content-Type", "text/javascript")
			if err := json.NewEncoder(w).Encode(camtypes.Discovery{
				BlobRoot: "/bs/",
				Signing:  signing,
			}); err != nil {
				t.Error(err)
			}
		case "/bs/camli/enumerate-blobs":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"blobs":[]}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &discoveries
}

func TestSyncKeyMismatchWarning(t *testing.T) {
	syncTestConfig(t)
	modern := func(id, fp string) *camtypes.SignDiscovery {
		return &camtypes.SignDiscovery{PublicKeyID: id, PublicKeyFingerprint: fp}
	}
	key := modern(syncTestKeyID, syncTestFingerprint)
	collision := modern(syncTestKeyID, syncTestCollisionFingerprint)
	other := modern(syncTestOtherKeyID, syncTestOtherFingerprint)
	legacy := modern(syncTestKeyID, "")
	otherLegacy := modern(syncTestOtherKeyID, "")
	tests := []struct {
		name       string
		src, dest  *camtypes.SignDiscovery
		destStdout bool
		srcDisk    bool
		destDisk   bool
		thirdDisk  bool
		wantKind   string
	}{
		{name: "equal fingerprints", src: key, dest: key},
		{name: "different fingerprints", src: key, dest: other, wantKind: "key fingerprint"},
		{name: "same key ID different fingerprints", src: key, dest: collision, wantKind: "key fingerprint"},
		{name: "fingerprints override different IDs", src: key, dest: modern(syncTestOtherKeyID, syncTestFingerprint)},
		{name: "equal legacy IDs", src: legacy, dest: legacy},
		{name: "different legacy IDs", src: legacy, dest: otherLegacy, wantKind: "key ID"},
		{name: "legacy source equal", src: legacy, dest: key},
		{name: "legacy destination equal", src: key, dest: legacy},
		{name: "legacy source different", src: otherLegacy, dest: key, wantKind: "key ID"},
		{name: "legacy destination different", src: key, dest: otherLegacy, wantKind: "key ID"},
		{name: "neither signs"},
		{name: "unsigned source modern destination", dest: key, wantKind: "key ID"},
		{name: "modern source unsigned destination", src: key, wantKind: "key ID"},
		{name: "unsigned source legacy destination", dest: legacy, wantKind: "key ID"},
		{name: "legacy source unsigned destination", src: legacy, wantKind: "key ID"},
		{name: "fingerprints without legacy IDs", src: modern("", syncTestFingerprint), dest: modern("", syncTestOtherFingerprint), wantKind: "key fingerprint"},
		{name: "stdout", src: key, destStdout: true},
		{name: "disk source", dest: key, srcDisk: true},
		{name: "disk destination", src: key, destDisk: true},
		{name: "disk third leg", src: key, dest: other, thirdDisk: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c syncCmd
			if tt.src != nil {
				c.srcKeyID, c.srcKeyFingerprint = tt.src.PublicKeyID, tt.src.PublicKeyFingerprint
			}
			if tt.dest != nil {
				c.destKeyID, c.destKeyFingerprint = tt.dest.PublicKeyID, tt.dest.PublicKeyFingerprint
			}
			if tt.destStdout {
				c.dest = "stdout"
			}
			c.oneIsDisk = tt.srcDisk || tt.destDisk || tt.thirdDisk
			var want string
			if tt.wantKind != "" {
				src, dest := c.srcKeyID, c.destKeyID
				if tt.wantKind == "key fingerprint" {
					src, dest = c.srcKeyFingerprint, c.destKeyFingerprint
				}
				want = fmt.Sprintf("WARNING: the source server GPG %s (%s) and the destination's (%s) differ.", tt.wantKind, src, dest)
			}
			warning := c.keyMismatchWarning()
			if want == "" {
				if warning != "" {
					t.Errorf("keyMismatchWarning() = %q; want no warning", warning)
				}
			} else if !strings.HasPrefix(warning, want) {
				t.Errorf("keyMismatchWarning() = %q; want prefix %q", warning, want)
			}

			// Also check the actual command wiring, including discovery,
			// fallback, disk/stdout suppression, and the deferred warning.
			src, srcDiscoveries := syncTestServer(t, tt.src)
			dest, destDiscoveries := syncTestServer(t, tt.dest)
			c = syncCmd{src: src.URL, dest: dest.URL, concurrency: 1}
			if tt.srcDisk {
				c.src = t.TempDir()
			}
			if tt.destDisk {
				c.dest = t.TempDir()
			}
			if tt.destStdout {
				c.dest = "stdout"
			}
			if tt.thirdDisk {
				c.third = t.TempDir()
			}
			var stdout, stderr bytes.Buffer
			oldStdout, oldStderr := cmdmain.Stdout, cmdmain.Stderr
			cmdmain.Stdout, cmdmain.Stderr = &stdout, &stderr
			t.Cleanup(func() { cmdmain.Stdout, cmdmain.Stderr = oldStdout, oldStderr })
			if err := c.RunCommand(nil); err != nil {
				t.Fatal(err)
			}
			if want == "" {
				if strings.Contains(stderr.String(), "WARNING:") {
					t.Errorf("unexpected warning: %s", &stderr)
				}
			} else {
				if got := strings.Count(stderr.String(), want); got != 2 {
					t.Errorf("warning count = %d; want 2; stderr: %s", got, &stderr)
				}
				if !strings.HasPrefix(stderr.String(), warning+"\n") || !strings.HasSuffix(stderr.String(), warning+"\n") {
					t.Errorf("warning not printed before and after sync: %s", &stderr)
				}
			}
			wantSrc, wantDest := int32(1), int32(1)
			if tt.srcDisk {
				wantSrc = 0
			}
			if tt.destDisk || tt.destStdout {
				wantDest = 0
			}
			if got := srcDiscoveries.Load(); got != wantSrc {
				t.Errorf("source discovery requests = %d; want %d", got, wantSrc)
			}
			if got := destDiscoveries.Load(); got != wantDest {
				t.Errorf("destination discovery requests = %d; want %d", got, wantDest)
			}
		})
	}
}

func TestSyncStorageFromParamDiscovery(t *testing.T) {
	syncTestConfig(t)
	tests := []struct {
		name    string
		signing *camtypes.SignDiscovery
	}{
		{"modern", &camtypes.SignDiscovery{PublicKeyID: syncTestKeyID, PublicKeyFingerprint: syncTestFingerprint}},
		{"legacy", &camtypes.SignDiscovery{PublicKeyID: syncTestKeyID}},
		{"no signing", nil},
		{"empty signing", &camtypes.SignDiscovery{}},
	}
	for _, tt := range tests {
		for _, which := range []storageType{storageSource, storageDest, storageThird} {
			t.Run(tt.name+"/"+string(which), func(t *testing.T) {
				srv, discoveries := syncTestServer(t, tt.signing)
				var stderr bytes.Buffer
				oldStderr := cmdmain.Stderr
				cmdmain.Stderr = &stderr
				t.Cleanup(func() { cmdmain.Stderr = oldStderr })
				c := syncCmd{}
				sto, err := c.storageFromParam(which, srv.URL)
				if err != nil {
					t.Fatal(err)
				}
				if _, ok := sto.(*client.Client); !ok {
					t.Fatalf("storage = %T; want *client.Client", sto)
				}
				var wantID, wantFingerprint string
				if tt.signing != nil {
					wantID, wantFingerprint = tt.signing.PublicKeyID, tt.signing.PublicKeyFingerprint
				}
				want := syncCmd{}
				switch which {
				case storageSource:
					want.srcKeyID, want.srcKeyFingerprint = wantID, wantFingerprint
				case storageDest:
					want.destKeyID, want.destKeyFingerprint = wantID, wantFingerprint
				}
				if c != want {
					t.Errorf("syncCmd = %+v; want %+v", c, want)
				}
				if stderr.Len() != 0 {
					t.Errorf("unexpected stderr: %s", &stderr)
				}
				if got := discoveries.Load(); got != 1 {
					t.Errorf("discovery requests = %d; want 1 for both key accessors", got)
				}
			})
		}
	}
}

func TestSyncStorageFromParamDiscoveryError(t *testing.T) {
	syncTestConfig(t)
	for _, status := range []int{http.StatusInternalServerError, http.StatusOK} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var discoveries atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				discoveries.Add(1)
				w.Header().Set("Content-Type", "text/javascript")
				w.WriteHeader(status)
				fmt.Fprint(w, "invalid discovery JSON")
			}))
			defer srv.Close()
			var stderr bytes.Buffer
			oldStderr := cmdmain.Stderr
			cmdmain.Stderr = &stderr
			t.Cleanup(func() { cmdmain.Stderr = oldStderr })
			c := syncCmd{}
			sto, err := c.storageFromParam(storageSource, srv.URL)
			if err != nil {
				t.Fatalf("discovery failure must remain nonfatal: %v", err)
			}
			if _, ok := sto.(*client.Client); !ok {
				t.Fatalf("storage = %T; want *client.Client", sto)
			}
			if c != (syncCmd{}) {
				t.Errorf("failed discovery populated signing keys: %+v", c)
			}
			if !strings.Contains(stderr.String(), "Failed to discover") || !strings.Contains(stderr.String(), srv.URL) {
				t.Errorf("missing discovery diagnostic: %s", &stderr)
			}
			if got := discoveries.Load(); got != 1 {
				t.Errorf("discovery requests = %d; want 1", got)
			}
		})
	}
}

func TestLooksLikePath(t *testing.T) {
	type pathTest struct {
		v    string
		want bool
	}
	tests := []pathTest{
		{"foo.com", false},
		{"127.0.0.1:234", false},
		{"foo", false},

		{"/foo", true},
		{"./foo", true},
		{"../foo", true},
	}
	if runtime.GOOS == "windows" {
		tests = append(tests,
			pathTest{`\foo`, true},
			pathTest{`.\foo`, true},
			pathTest{`..\foo`, true},
			pathTest{`C:/dir`, true},
			pathTest{`C:\dir`, true},
			pathTest{`//server/share/dir`, true},
			pathTest{`\\server\share\dir`, true},
		)
	}
	for _, tt := range tests {
		got := looksLikePath(tt.v)
		if got != tt.want {
			t.Errorf("looksLikePath(%q) = %v; want %v", tt.v, got, tt.want)
		}
	}
}
