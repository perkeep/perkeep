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

package serverinit_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/health"
	"perkeep.org/pkg/serverinit"
)

// TestReadOnlyRefusesWrites checks that while the health monitor reports
// a fatal failure, the blob handlers refuse uploads but still serve reads.
func TestReadOnlyRefusesWrites(t *testing.T) {
	mux, mon := newTestServer(t)

	const contents = "some blob"
	br := blob.RefFromString(contents)
	do := func(method string, authed bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "/bs/camli/"+br.String(), strings.NewReader(contents))
		if authed {
			req.SetBasicAuth("foo", "bar")
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	check := mon.Register("test.fatal[test]", "test check")
	check.Fatal(errors.New("something is very wrong"))

	if rec := do("PUT", false); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated PUT while read-only = %d; want %d", rec.Code, http.StatusUnauthorized)
	}
	rec := do("PUT", true)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("PUT while read-only = %d: %s; want %d", rec.Code, rec.Body, http.StatusServiceUnavailable)
	}
	if !strings.Contains(rec.Body.String(), "something is very wrong") {
		t.Errorf("PUT while read-only said %q; want it to give the reason", rec.Body)
	}
	if rec := do("GET", true); rec.Code != http.StatusNotFound {
		t.Errorf("GET of missing blob while read-only = %d; want %d", rec.Code, http.StatusNotFound)
	}

	check.OK()
	if rec := do("PUT", true); rec.Code >= 300 {
		t.Fatalf("PUT after the check is OK = %d: %s; want success", rec.Code, rec.Body)
	}
	if rec := do("GET", true); rec.Code != http.StatusOK || rec.Body.String() != contents {
		t.Errorf("GET after upload = %d %q; want 200 %q", rec.Code, rec.Body, contents)
	}
}

// newTestServer returns the handlers of a server with the default
// config, in memory, with user "foo" and password "bar", and its health
// monitor.
func newTestServer(t *testing.T) (*http.ServeMux, *health.Monitor) {
	t.Helper()
	srcRoot, err := osutil.PkSourceRoot()
	if err != nil {
		t.Fatalf("source root folder not found: %v", err)
	}
	conf := serverinit.DefaultBaseConfig
	conf.Auth = "userpass:foo:bar"
	conf.Identity = "26F5ABDA"
	conf.IdentitySecretRing = filepath.Join(srcRoot, filepath.FromSlash("pkg/jsonsign/testdata/test-secring.gpg"))
	conf.MemoryStorage = true
	conf.MemoryIndex = true
	// Also checks that the low-level config can have healthState.
	conf.HealthState = map[string]any{"type": "memory"}
	confData, err := json.Marshal(conf)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAMLI_CONFIG_DIR", "whatever")
	lowConf, err := serverinit.Load(confData)
	if err != nil {
		t.Fatal(err)
	}
	if got := lowConf.HealthState()["type"]; got != "memory" {
		t.Errorf("HealthState type = %v; want memory", got)
	}
	mon := health.New()
	lowConf.SetHealth(mon)
	mux := http.NewServeMux()
	shutdown, err := lowConf.InstallHandlers(mux, "http://"+conf.Listen)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := shutdown.Close(); err != nil {
			t.Error(err)
		}
	})
	return mux, mon
}
