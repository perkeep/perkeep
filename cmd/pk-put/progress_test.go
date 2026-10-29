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
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"perkeep.org/pkg/upload"
)

func TestProgressServer(t *testing.T) {
	ps := newProgressServer(nil)
	get := func(path string) string {
		t.Helper()
		rec := httptest.NewRecorder()
		ps.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 200 {
			t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body)
		}
		return rec.Body.String()
	}
	if body := get("/"); !strings.Contains(body, "No directory upload yet") {
		t.Errorf("page before any upload:\n%s", body)
	}

	tr := upload.NewTree(nil, fstest.MapFS{"photos/a.jpg": {Data: []byte("a")}}, "photos", nil)
	done := ps.add("photos <&>", tr)
	if body := get("/"); !strings.Contains(body, "photos &lt;&amp;&gt;") || !strings.Contains(body, "Running for") {
		t.Errorf("page during upload:\n%s", body)
	}
	done(errors.New("oops"))
	if body := get("/"); !strings.Contains(body, "Finished in") || !strings.Contains(body, "Error: oops") {
		t.Errorf("page after upload:\n%s", body)
	}
	var st progressStatus
	if err := json.Unmarshal([]byte(get("/json")), &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Trees) != 1 || st.Trees[0].Dir != "photos <&>" || st.Trees[0].Err != "oops" {
		t.Errorf("JSON trees = %+v", st.Trees)
	}
}
