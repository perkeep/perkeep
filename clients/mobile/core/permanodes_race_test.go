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

package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"perkeep.org/pkg/blob"
)

// TestPermanodeOnceForSameContents checks that files with the same
// contents, uploaded at once, get one permanode with one claim.
func TestPermanodeOnceForSameContents(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t, t.TempDir())
	s.testClient = newTestPerkeep(t, nil)
	var a Account
	do(t, s, "POST", "/accounts", accountFields{Server: "example.com"}, http.StatusCreated, &a)
	path := "/accounts/" + a.ID
	do(t, s, "POST", path+"/connect", map[string]string{}, http.StatusOK, nil)
	ac, err := s.conn(a.ID)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	first := writeRandomFile(t, dir, "one.jpg", 300<<10)
	data, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(dir, "two.jpg")
	if err := os.WriteFile(second, data, 0o644); err != nil {
		t.Fatal(err)
	}
	var refs struct{ Refs []string }
	do(t, s, "POST", path+"/hashes/file", map[string]string{"key": "one", "path": first}, http.StatusOK, &refs)
	for i, p := range []string{first, second} {
		do(t, s, "POST", path+"/uploads", map[string]any{"key": fmt.Sprint(i), "path": p, "refs": refs.Refs, "noDupCheck": true}, http.StatusOK, nil)
	}
	ac.running.Wait()
	var st uploadsStatus
	do(t, s, "GET", path+"/uploads", nil, http.StatusOK, &st)
	for _, d := range st.Done {
		if d.Error != "" {
			t.Fatalf("upload: %v", d.Error)
		}
	}
	pn := blob.RefFromString(plannedPermanodeOfTestKey(t, refs.Refs))
	sr, err := ac.c.SearchRoot()
	if err != nil {
		t.Fatal(err)
	}
	var claims struct{ Claims []any }
	if err := ac.c.GetJSON(ctx, sr+"camli/search/claims?permanode="+pn.String(), &claims); err != nil {
		t.Fatal(err)
	}
	if len(claims.Claims) != 1 {
		t.Errorf("%d claims on the permanode of two files with the same contents; want 1", len(claims.Claims))
	}
}
