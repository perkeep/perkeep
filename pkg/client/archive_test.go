/*
Copyright 2026 The Perkeep Authors.

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
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/types/camtypes"
)

func TestArchiveAPI(t *testing.T) {
	fileRef := blob.RefFromString("archive file schema")
	accountRef := blob.RefFromString("account")
	var actions []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/prefix/":
			w.Header().Set("Content-Type", "text/javascript")
			json.NewEncoder(w).Encode(camtypes.Discovery{BlobRoot: "/prefix/blobs/", ImporterRoot: "imports/"})
		case "/prefix/imports/runkeeper/archive":
			if r.Method == http.MethodPost {
				if got := r.Header.Get("Content-Type"); got != "application/json" {
					t.Errorf("Content-Type = %q", got)
				}
				var req camtypes.ArchiveImportRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				if req.FileRef != fileRef {
					t.Errorf("fileRef = %v", req.FileRef)
				}
				actions = append(actions, req.Action)
				json.NewEncoder(w).Encode(camtypes.ArchiveImportResult{
					Identity: camtypes.ArchiveIdentity{UserName: "runner"}, AccountRef: accountRef,
					Status: &camtypes.ArchiveImportStatus{FileRef: fileRef, AccountRef: accountRef, State: "running"},
				})
			} else {
				if got := r.URL.Query().Get("account"); got != accountRef.String() {
					t.Errorf("account = %q", got)
				}
				json.NewEncoder(w).Encode(camtypes.ArchiveImportStatus{State: "complete", FileRef: fileRef, AccountRef: accountRef})
			}
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	c, err := New(OptionServer(ts.URL+"/prefix/"), OptionNoExternalConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	if result, err := c.InspectArchive(ctx, "runkeeper", fileRef); err != nil || result.Identity.UserName != "runner" {
		t.Fatalf("inspect = %v, %v", result, err)
	}
	if result, err := c.ImportArchive(ctx, "runkeeper", fileRef); err != nil || result.AccountRef != accountRef {
		t.Fatalf("import = %v, %v", result, err)
	}
	if status, err := c.ArchiveImportStatus(ctx, "runkeeper", accountRef); err != nil || status.State != "complete" {
		t.Fatalf("status = %v, %v", status, err)
	}
	if strings.Join(actions, ",") != "inspect,start" {
		t.Errorf("actions = %v", actions)
	}
	for _, name := range []string{"", "../runkeeper", "a/b", "a?b", "a#b"} {
		if _, err := c.ImportArchive(ctx, name, fileRef); err == nil {
			t.Errorf("accepted importer name %q", name)
		}
	}
	if _, err := c.ImportArchive(ctx, "runkeeper", blob.Ref{}); err == nil {
		t.Error("accepted empty file ref")
	}
}

func TestArchiveAPIErrors(t *testing.T) {
	for _, test := range []struct {
		name, body string
		code       int
	}{
		{"server error", "ambiguous account", http.StatusConflict},
		{"invalid JSON", "not JSON", http.StatusOK},
		{"large metadata", strings.Repeat(" ", (1<<20)+1), http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.code)
				w.Write([]byte(test.body))
			}))
			defer ts.Close()
			c, err := New(OptionServer(ts.URL), OptionNoExternalConfig())
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			c.discoOnce.Do(func() error { c.importerRoot = ts.URL; return nil })
			if _, err := c.ImportArchive(context.Background(), "runkeeper", blob.RefFromString("file")); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
