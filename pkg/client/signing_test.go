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

package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/jsonsign"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/types/camtypes"
)

const (
	testSigningKeyID       = "2931A67C26F5ABDA"
	testSigningFingerprint = "FBB89AA320A2806FE497C0492931A67C26F5ABDA"
)

func TestSigningDiscovery(t *testing.T) {
	for _, tt := range []struct {
		name                          string
		signing                       *camtypes.SignDiscovery
		wantID, wantFingerprint       string
		wantIDErr, wantFingerprintErr error
	}{
		{
			name:    "legacy",
			signing: &camtypes.SignDiscovery{PublicKeyID: testSigningKeyID, SignHandler: "/sig/"},
			wantID:  testSigningKeyID,
		},
		{
			name:    "fingerprint",
			signing: &camtypes.SignDiscovery{PublicKeyID: testSigningKeyID, PublicKeyFingerprint: testSigningFingerprint, SignHandler: "/sig/"},
			wantID:  testSigningKeyID, wantFingerprint: testSigningFingerprint,
		},
		{
			name:      "no signing",
			wantIDErr: ErrNoSigning, wantFingerprintErr: ErrNoSigning,
		},
		{
			name:      "signing without key metadata",
			signing:   &camtypes.SignDiscovery{SignHandler: "/sig/"},
			wantIDErr: ErrNoSigning,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var requests atomic.Int32
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if got := r.Header.Get("Accept"); got != "text/x-camli-configuration" {
					t.Errorf("Accept = %q", got)
				}
				w.Header().Set("Content-Type", "text/javascript")
				json.NewEncoder(w).Encode(camtypes.Discovery{BlobRoot: "/bs/", Signing: tt.signing})
			}))
			defer ts.Close()
			c, err := New(OptionServer(ts.URL), OptionAuthMode(auth.None{}))
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				fp, err := c.ServerKeyFingerprint()
				if fp != tt.wantFingerprint || !errors.Is(err, tt.wantFingerprintErr) {
					t.Errorf("ServerKeyFingerprint() = %q, %v; want %q, %v", fp, err, tt.wantFingerprint, tt.wantFingerprintErr)
				}
				id, err := c.ServerKeyID()
				if id != tt.wantID || !errors.Is(err, tt.wantIDErr) {
					t.Errorf("ServerKeyID() = %q, %v; want %q, %v", id, err, tt.wantID, tt.wantIDErr)
				}
			}
			if n := requests.Load(); n != 1 {
				t.Errorf("discovery requests = %d; want 1", n)
			}
		})
	}
}

func TestSigningDiscoveryError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "discovery unavailable", http.StatusInternalServerError)
	}))
	defer ts.Close()
	c, err := New(OptionServer(ts.URL), OptionAuthMode(auth.None{}))
	if err != nil {
		t.Fatal(err)
	}
	if fp, err := c.ServerKeyFingerprint(); fp != "" || err == nil || errors.Is(err, ErrNoSigning) {
		t.Errorf("ServerKeyFingerprint() = %q, %v; want discovery error", fp, err)
	}
	if id, err := c.ServerKeyID(); id != "" || err == nil || errors.Is(err, ErrNoSigning) {
		t.Errorf("ServerKeyID() = %q, %v; want discovery error", id, err)
	}
}

func TestCheckMatchingKeys(t *testing.T) {
	ent, err := jsonsign.EntityFromSecring(testSigningKeyID, "../jsonsign/testdata/test-secring.gpg")
	if err != nil {
		t.Fatal(err)
	}
	armored, err := jsonsign.ArmoredPublicKey(ent)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := schema.NewSigner(blob.RefFromString(armored), strings.NewReader(armored), ent)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name        string
		signing     *camtypes.SignDiscovery
		wantWarning string
	}{
		{name: "legacy match", signing: &camtypes.SignDiscovery{PublicKeyID: testSigningKeyID}},
		{name: "legacy mismatch", signing: &camtypes.SignDiscovery{PublicKeyID: "0000000000000000"}, wantWarning: "keys differ"},
		{name: "fingerprint match", signing: &camtypes.SignDiscovery{PublicKeyID: testSigningKeyID, PublicKeyFingerprint: testSigningFingerprint}},
		// The full fingerprint must win even when the long key IDs agree.
		{name: "fingerprint mismatch same ID", signing: &camtypes.SignDiscovery{PublicKeyID: testSigningKeyID, PublicKeyFingerprint: "0000000000000000000000002931A67C26F5ABDA"}, wantWarning: "keys differ"},
		// Conversely, an inconsistent legacy ID must not override a matching fingerprint.
		{name: "prefer matching fingerprint", signing: &camtypes.SignDiscovery{PublicKeyID: "0000000000000000", PublicKeyFingerprint: testSigningFingerprint}},
		{name: "no signing", wantWarning: "server doesn't support signing"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/javascript")
				json.NewEncoder(w).Encode(camtypes.Discovery{BlobRoot: "/bs/", Signing: tt.signing})
			}))
			defer ts.Close()
			c, err := New(OptionServer(ts.URL), OptionAuthMode(auth.None{}))
			if err != nil {
				t.Fatal(err)
			}
			c.signer = signer
			var output bytes.Buffer
			prev := log.Writer()
			log.SetOutput(&output)
			defer log.SetOutput(prev)
			c.checkMatchingKeys()
			got := output.String()
			if tt.wantWarning == "" {
				if got != "" {
					t.Errorf("unexpected warning: %s", got)
				}
			} else if !strings.Contains(got, tt.wantWarning) {
				t.Errorf("warning = %q; want %q", got, tt.wantWarning)
			}
			if strings.Contains(got, "keys differ") && tt.signing.PublicKeyFingerprint != "" {
				if !strings.Contains(got, signer.Fingerprint()) || !strings.Contains(got, tt.signing.PublicKeyFingerprint) {
					t.Errorf("warning must show full fingerprints: %s", got)
				}
			}
		})
	}
}
