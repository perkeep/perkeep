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
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/index/indextest"
	. "perkeep.org/pkg/search"
	"perkeep.org/pkg/test"
	"perkeep.org/pkg/types/camtypes"
)

func TestContentPermanodes(t *testing.T) {
	for _, mode := range []string{"index", "corpus-build", "corpus-scan"} {
		t.Run(mode, func(t *testing.T) { testContentPermanodes(t, mode) })
	}
}

// testContentPermanodes tests a handler with a plain index, a corpus built
// as the index is, or a corpus scanned from it after.
func testContentPermanodes(t *testing.T, mode string) {
	defer index.SetVerboseCorpusLogging(true)
	index.SetVerboseCorpusLogging(false)
	idx := index.NewMemoryIndex()
	idx.Logger = test.NewLogger(t, "")
	var corpus *index.Corpus
	if mode == "corpus-build" {
		var err error
		if corpus, err = idx.KeepInMemory(); err != nil {
			t.Fatal(err)
		}
	}
	id := indextest.NewIndexDeps(idx)
	now := time.Unix(1_800_000_000, 0)

	// Two files with the same contents, one with a permanode.
	fileA, wholeA := id.UploadFile("a.jpg", "contents of a", now)
	id.UploadFile("a copy.jpg", "contents of a", now)
	pnA := id.NewDeterministicPermanodeForTest("pn-a")
	id.SetAttribute(pnA, "camliContent", fileA.String())

	// A file whose other file, with the same contents, has a permanode.
	_, wholeB := id.UploadFile("b.jpg", "contents of b", now)
	fileB2, _ := id.UploadFile("b copy.jpg", "contents of b", now)
	pnB := id.NewDeterministicPermanodeForTest("pn-b")
	id.SetAttribute(pnB, "camliContent", fileB2.String())

	// A file without a permanode.
	_, wholeC := id.UploadFile("c.jpg", "contents of c", now)

	// A file whose permanode's content has changed to another file.
	fileD, wholeD := id.UploadFile("d.jpg", "contents of d", now)
	fileE, wholeE := id.UploadFile("e.jpg", "contents of e", now)
	pnD := id.NewDeterministicPermanodeForTest("pn-d")
	id.SetAttribute(pnD, "camliContent", fileD.String())
	id.SetAttribute(pnD, "camliContent", fileE.String())

	unknown := blob.RefFromString("not uploaded")

	h := NewHandler(idx, index.NewOwner(indextest.KeyID, id.SignerBlobRef))
	if mode == "corpus-scan" {
		var err error
		if corpus, err = idx.KeepInMemory(); err != nil {
			t.Fatal(err)
		}
		idx.PreventStorageAccessForTesting()
	}
	if corpus != nil {
		h.SetCorpus(corpus)
	}
	ask := func(t *testing.T, method string, refs ...string) camtypes.ContentPermanodesResponse {
		t.Helper()
		v := url.Values{"wholedigest": refs}
		var req *http.Request
		if method == "GET" {
			req = httptest.NewRequest("GET", "/camli/search/contentpermanodes?"+v.Encode(), nil)
		} else {
			req = httptest.NewRequest("POST", "/camli/search/contentpermanodes", strings.NewReader(v.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		req.Header.Set(httputil.PathSuffixHeader, req.URL.Path[1:])
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		var res camtypes.ContentPermanodesResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
			t.Fatalf("decoding %s: %v", rr.Body, err)
		}
		return res
	}

	want := map[string][]blob.Ref{
		wholeA.String(): {pnA},
		wholeB.String(): {pnB},
		wholeE.String(): {pnD},
	}
	for _, method := range []string{"GET", "POST"} {
		t.Run(method, func(t *testing.T) {
			res := ask(t, method, wholeA.String(), wholeB.String(), wholeC.String(), wholeD.String(), wholeE.String(), unknown.String())
			if res.Error != "" {
				t.Fatalf("error: %v", res.Error)
			}
			if !reflect.DeepEqual(res.Permanodes, want) {
				t.Errorf("permanodes = %v; want %v", res.Permanodes, want)
			}
		})
	}

	t.Run("errors", func(t *testing.T) {
		if res := ask(t, "GET"); res.ErrorType != "input" {
			t.Errorf("no digests: %+v", res)
		}
		if res := ask(t, "GET", "bogus"); res.ErrorType != "input" {
			t.Errorf("bad digest: %+v", res)
		}
		many := make([]string, 1001)
		for i := range many {
			many[i] = unknown.String()
		}
		if res := ask(t, "POST", many...); res.ErrorType != "input" {
			t.Errorf("too many digests: %+v", res)
		}
		if res := ask(t, "GET", unknown.String()); res.Error != "" || res.Permanodes == nil || len(res.Permanodes) != 0 {
			t.Errorf("unknown digest: %+v; want an empty map", res)
		}
	})
}
