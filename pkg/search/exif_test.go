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

package search_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/index/indextest"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/types/camtypes"
)

func TestEXIFHTTP(t *testing.T) {
	auth.SetMode(auth.NewBasicAuth("exif-test", "password"))
	t.Cleanup(func() { auth.SetMode(auth.None{}) })
	for _, corpus := range []bool{false, true} {
		t.Run(fmt.Sprintf("corpus=%v", corpus), func(t *testing.T) {
			kv := sorted.NewMemoryKeyValue()
			idx, err := index.New(kv)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { idx.Close() })
			set := func(k, v string) {
				t.Helper()
				if err := kv.Set(k, v); err != nil {
					t.Fatal(err)
				}
			}
			file, whole := blob.RefFromString("photo file"), blob.RefFromString("photo whole")
			plain, dir := blob.RefFromString("plain file"), blob.RefFromString("directory")
			malformed := blob.RefFromString("malformed optional EXIF")
			for _, ref := range []blob.Ref{file, plain, malformed, dir} {
				typ := "file"
				if ref == dir {
					typ = "directory"
				}
				set("meta:"+ref.String(), "123|application/json; camliType="+typ)
				content := ref
				if ref == file {
					content = whole
				}
				set("fileinfo|"+ref.String(), "1|photo.jpg|image%2Fjpeg|"+content.String())
			}
			set("meta:"+whole.String(), "100|image/jpeg")
			for suffix, value := range map[string]string{
				"0|010f": "string|1|Samsung",
				"0|0112": "int|1|1",
				"1|0112": "int|1|6",
				"2|829a": "rat|1|123456789/987654321",
				"2|c001": "float|1|1.2345678901234567",
				"3|0002": "rat|3|12/1|30/1|0/1",
				"3|c001": "string|1|%3Cscript%3E%22%26%2B%7C%25%E6%97%A5%E6%9C%AC%00",
			} {
				set("exiftag|"+whole.String()+"|"+suffix, value)
			}
			set("exiftag|"+malformed.String()+"|0|010f", "string|1|bad%")
			set("exiftag|"+malformed.String()+"|2|829a", "rat|2|1/2")

			h := search.NewHandler(idx, owner)
			if corpus {
				idx.Lock()
				c, err := idx.KeepInMemory()
				idx.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				h.SetCorpus(c)
			}
			// No blob source is installed: all responses must come from
			// indexed rows, never from fetching the image or schema blob.
			const root = "/custom-search/"
			handler := auth.Handler{Handler: &httputil.PrefixHandler{Prefix: root, Handler: h}}
			request := func(query string, authenticated bool) *httptest.ResponseRecorder {
				req := httptest.NewRequest("GET", root+"camli/search/"+query, nil)
				if authenticated {
					req.SetBasicAuth("exif-test", "password")
				}
				rw := httptest.NewRecorder()
				handler.ServeHTTP(rw, req)
				return rw
			}
			rw := request("exif?blobref="+file.String(), false)
			if rw.Code != http.StatusUnauthorized {
				t.Fatalf("unauthenticated status = %d; want 401", rw.Code)
			}
			rw = request("exif?blobref="+url.QueryEscape(file.String()), true)
			if rw.Code != http.StatusOK {
				t.Fatalf("EXIF status = %d: %s", rw.Code, rw.Body.String())
			}
			want := camtypes.EXIFInfo{
				FileRef: file, WholeRef: whole,
				Tags: []camtypes.EXIFTag{
					{Directory: "IFD0", Tag: "010f", Name: "Make", Type: "string", Values: []string{"Samsung"}},
					{Directory: "IFD0", Tag: "0112", Name: "Orientation", Type: "int", Values: []string{"1"}},
					{Directory: "IFD1", Tag: "0112", Name: "Orientation", Type: "int", Values: []string{"6"}},
					{Directory: "Exif", Tag: "829a", Name: "ExposureTime", Type: "rat", Values: []string{"123456789/987654321"}},
					{Directory: "Exif", Tag: "c001", Name: "Unknown tag", Type: "float", Values: []string{"1.2345678901234567"}},
					{Directory: "GPS", Tag: "0002", Name: "GPSLatitude", Type: "rat", Values: []string{"12/1", "30/1", "0/1"}},
					{Directory: "GPS", Tag: "c001", Name: "Unknown tag", Type: "string", Values: []string{"<script>\"&+|%日本\x00"}},
				},
			}
			var got camtypes.EXIFInfo
			if err := json.Unmarshal(rw.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("EXIF = %+v; want %+v", got, want)
			}
			if strings.Contains(rw.Body.String(), "<script>") {
				t.Error("JSON contains unescaped HTML")
			}
			// Subsequent requests have the same tag ordering.
			if second := request("exif?blobref="+file.String(), true); second.Body.String() != rw.Body.String() {
				t.Error("EXIF JSON is not deterministic")
			}
			for _, ref := range []blob.Ref{plain, malformed} {
				rw := request("exif?blobref="+ref.String(), true)
				var got camtypes.EXIFInfo
				if err := json.Unmarshal(rw.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if rw.Code != http.StatusOK || got.FileRef != ref || got.Tags == nil || len(got.Tags) != 0 ||
					!strings.Contains(rw.Body.String(), `"tags": []`) {
					t.Errorf("empty EXIF = %d %s; want 200 and tags:[]", rw.Code, rw.Body.String())
				}
			}
			for _, tt := range []struct {
				query string
				code  int
			}{
				{"exif", http.StatusBadRequest},
				{"exif?blobref=", http.StatusBadRequest},
				{"exif?blobref=not-a-ref", http.StatusBadRequest},
				{"exif?blobref=" + blob.RefFromString("missing").String(), http.StatusNotFound},
				{"exif?blobref=" + dir.String(), http.StatusNotFound},
				{"exif?blobref=" + whole.String(), http.StatusNotFound},
			} {
				rw := request(tt.query, true)
				if rw.Code != tt.code {
					t.Errorf("%s = %d %s; want %d", tt.query, rw.Code, rw.Body.String(), tt.code)
				}
				var body map[string]any
				if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil || body["error"] == nil {
					t.Errorf("%s did not return a JSON error: %s", tt.query, rw.Body.String())
				}
			}
			described := request("describe?blobref="+file.String(), true)
			if described.Code != http.StatusOK || !strings.Contains(described.Body.String(), `"fileName"`) {
				t.Fatalf("describe = %d %s", described.Code, described.Body.String())
			}
			for _, unexpected := range []string{`"exif"`, `"tags"`, `"directory"`, "Samsung", "123456789/987654321"} {
				if strings.Contains(described.Body.String(), unexpected) {
					t.Errorf("default describe includes EXIF %q: %s", unexpected, described.Body.String())
				}
			}
		})
	}
}

func TestEXIFHTTPIndexedImage(t *testing.T) {
	for _, corpus := range []bool{false, true} {
		t.Run(fmt.Sprintf("corpus=%v", corpus), func(t *testing.T) {
			idx := index.NewMemoryIndex()
			t.Cleanup(func() { idx.Close() })
			id := indextest.NewIndexDeps(idx)
			id.Fataler = t
			file, whole := id.UploadFile("camera.jpg", cameraJPEG(t, "Samsung", "Galaxy Nexus"), time.Time{})
			h := search.NewHandler(idx, owner)
			if corpus {
				idx.Lock()
				c, err := idx.KeepInMemory()
				idx.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				h.SetCorpus(c)
			}
			req := httptest.NewRequest("GET", "/camli/search/exif?blobref="+file.String(), nil)
			req.Header.Set(httputil.PathSuffixHeader, "camli/search/exif")
			rw := httptest.NewRecorder()
			h.ServeHTTP(rw, req)
			var got camtypes.EXIFInfo
			if err := json.Unmarshal(rw.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			want := camtypes.EXIFInfo{
				FileRef: file, WholeRef: whole,
				Tags: []camtypes.EXIFTag{
					{Directory: "IFD0", Tag: "010f", Name: "Make", Type: "string", Values: []string{"Samsung"}},
					{Directory: "IFD0", Tag: "0110", Name: "Model", Type: "string", Values: []string{"Galaxy Nexus"}},
				},
			}
			if rw.Code != http.StatusOK || !reflect.DeepEqual(got, want) {
				t.Errorf("indexed EXIF = %d %+v; want %+v", rw.Code, got, want)
			}
		})
	}
}
