/*
Copyright 2014 The Perkeep Authors

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

package importer

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"

	"go4.org/jsonconfig"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/test"
)

func init() {
	Register("dummy1", TODOImporter)
	Register("dummy2", TODOImporter)
}

func TestStaticConfig(t *testing.T) {

	ld := test.NewLoader()
	h, err := newFromConfig(ld, jsonconfig.Obj{
		"dummy1": map[string]any{
			"clientID":     "id1",
			"clientSecret": "secret1",
		},
		"dummy2": map[string]any{
			"clientSecret": "id2:secret2",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	host := h.(*Host)
	t.Cleanup(func() { host.Close() })
	if g, w := host.imp["dummy1"].clientID, "id1"; g != w {
		t.Errorf("dummy1 id = %q; want %q", g, w)
	}
	if g, w := host.imp["dummy1"].clientSecret, "secret1"; g != w {
		t.Errorf("dummy1 secret = %q; want %q", g, w)
	}
	if g, w := host.imp["dummy2"].clientID, "id2"; g != w {
		t.Errorf("dummy2 id = %q; want %q", g, w)
	}
	if g, w := host.imp["dummy2"].clientSecret, "secret2"; g != w {
		t.Errorf("dummy2 secret = %q; want %q", g, w)
	}

	if _, err := newFromConfig(ld, jsonconfig.Obj{"dummy1": map[string]any{"bogus": ""}}); err == nil {
		t.Errorf("expected error from unknown key")
	}

	if _, err := newFromConfig(ld, jsonconfig.Obj{"dummy1": map[string]any{"clientSecret": "x"}}); err == nil {
		t.Errorf("expected error from secret without id")
	}
}

func TestImportRootPageHTML(t *testing.T) {
	h, err := NewHost(HostConfig{})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/importer/", nil)
	h.serveImportersRoot(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "dummy1") {
		t.Errorf("Got %d response with header %v, body %s", w.Code, w.Result().Header, w.Body.String())
	}
}

func TestImporterNavigationPaths(t *testing.T) {
	for _, tt := range []struct {
		name, baseURL, prefix, want string
	}{
		{"default", "https://configured.example:3179", "/importer/", "/importer/"},
		{"custom", "https://configured.example", "/custom/imports/", "/custom/imports/"},
		{"base path", "https://configured.example/perkeep", "/importer/", "/perkeep/importer/"},
		{"escaped path", "https://configured.example/per%20keep", "/importer/", "/per%20keep/importer/"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h, err := NewHost(HostConfig{BaseURL: tt.baseURL, Prefix: tt.prefix})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { h.Close() })
			im := h.imp["dummy1"]
			ref := blob.RefFromString("account")
			ia := &importerAcct{im: im, acct: &Object{pn: ref}}
			if got, want := h.ImporterBasePath(), tt.want; got != want {
				t.Errorf("ImporterBasePath = %q; want %q", got, want)
			}
			if got, want := ia.AccountURL(), tt.want+"dummy1/"+ref.String(); got != want {
				t.Errorf("AccountURL = %q; want %q", got, want)
			}
			// Render a non-TODO importer without depending on which
			// concrete importers happen to be linked into this test.
			props := *im.props
			props.TODOIssue = 0
			im.props = &props
			var buf bytes.Buffer
			if err := h.tmpl.ExecuteTemplate(&buf, "importersRootBody", importersRootBody{
				Host: h, Importers: []*importer{im},
			}); err != nil {
				t.Fatal(err)
			}
			if want := `href="` + tt.want + `dummy1"`; !strings.Contains(buf.String(), want) {
				t.Errorf("importer list missing %s:\n%s", want, &buf)
			}
		})
	}
}
