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

package importer

import (
	"strings"
	"testing"

	"perkeep.org/pkg/blob"
)

func TestAccountsTable(t *testing.T) {
	h, err := NewHost(HostConfig{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	h.uiPrefix = "/ui/"
	ia := &importerAcct{im: h.imp["dummy1"], acct: &Object{pn: blob.RefFromString("account")}}
	root := blob.RefFromString("root")
	var buf strings.Builder
	if err := h.tmpl.ExecuteTemplate(&buf, "accountsTable", accountsTableData{
		Rows: []accountRow{{Acct: ia, Root: root}},
	}); err != nil {
		t.Fatal(err)
	}
	// An account without a name yet still needs a link, to set it up or
	// delete it.
	if want := `<a href="` + ia.AccountURL() + `">(unnamed)</a>`; !strings.Contains(buf.String(), want) {
		t.Errorf("no %s in:\n%s", want, buf.String())
	}
	if want := `href="/ui/` + root.String() + `"`; !strings.Contains(buf.String(), want) {
		t.Errorf("no %s in:\n%s", want, buf.String())
	}
}
