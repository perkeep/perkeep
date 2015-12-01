/*
Copyright 2014 The Camlistore Authors

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
	"testing"

	"camlistore.org/pkg/test"
	"go4.org/jsonconfig"
)

func init() {
	Register("dummy1", TODOImporter)
	Register("dummy2", TODOImporter)
}

func TestStaticConfig(t *testing.T) {
	ld := test.NewLoader()
	h, err := newFromConfig(ld, jsonconfig.Obj{
		"dummy1": map[string]interface{}{
			"clientID":     "id1",
			"clientSecret": "secret1",
		},
		"dummy2": map[string]interface{}{
			"clientSecret": "id2:secret2",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	host := h.(*Host)
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

	if _, err := newFromConfig(ld, jsonconfig.Obj{"dummy1": map[string]interface{}{"bogus": ""}}); err == nil {
		t.Errorf("expected error from unknown key")
	}

	if _, err := newFromConfig(ld, jsonconfig.Obj{"dummy1": map[string]interface{}{"clientSecret": "x"}}); err == nil {
		t.Errorf("expected error from secret without id")
	}
}
