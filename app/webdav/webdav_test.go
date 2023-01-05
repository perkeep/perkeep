/*
Copyright 2022 The Perkeep Authors.

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

package main // import "perkeep.org/app/webdav"

import (
	"encoding/xml"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/test"
)

var (
	w  *test.World
	wr blob.Ref
)

func TestMain(m *testing.M) {
	flag.Parse()

	if testing.Short() {
		log.Println("Skipping WebDAV App tests in short mode")
		os.Exit(0)
	}

	var err error
	if w, err = test.NewWorld(); err != nil {
		log.Fatal(err)
	}
	if err = w.Start(); err != nil {
		log.Fatal(err)
	}
	defer w.Stop()

	// find webdav root, it is automatically added since we start perkeepd in dev mode,
	// the value of the attribute comes from pkg/test/testdata/server-config.json
	cmd := w.Cmd("pk", "search", "-1", "attr:camliRoot:dev-webdav-root")
	out, err := cmd.Output()
	if err != nil {
		log.Fatalf("unable to run pk-search to find webdav root: %v", err)
	}
	wr = blob.ParseOrZero(strings.TrimSpace(string(out)))
	if !wr.Valid() {
		log.Fatalf("invalid webdav root blobref from pk-search: %s", string(out))
	}

	m.Run()
}

func TestWebdav(t *testing.T) {
	const (
		testContent = "hello world"
	)
	if err := w.Ping(); err != nil {
		t.Fatalf("unable to ping world: %v", err)
	}

	tmpFile, err := os.CreateTemp(os.TempDir(), "webdav-test-*")
	if err != nil {
		t.Fatalf("unable to create tempfile: %v", err)
	}
	if _, err = tmpFile.WriteString(testContent); err != nil {
		t.Fatalf("unable to write test content to tempfile: %v", err)
	}
	br := w.PutFile(t, tmpFile.Name())

	// add uploaded blobref to webdav root
	cmd := w.Cmd("pk-put", "attr", "--add", wr.String(), "camliMember", br.String())
	if err := cmd.Run(); err != nil {
		t.Fatalf("unable to add tempdir to webdav root: %v", err)
	}

	// check content from webdav response
	baseName := filepath.Base(tmpFile.Name())
	r, err := http.Get(w.ServerBaseURL() + fmt.Sprintf("/webdav/%s", baseName))
	if err != nil {
		t.Fatalf("unable to get file: %v", err)
	}
	defer r.Body.Close()

	if r.StatusCode != http.StatusOK {
		t.Fatalf("unable to get file: got status %v", r.Status)
	}
	b, err := ioutil.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("unable to read webdav response: %v", err)
	}
	if have, want := string(b), testContent; have != want {
		t.Fatalf("unexpected file content, have %s, want %s", have, want)
	}

	// check props of the webdav response
	req, err := http.NewRequest("PROPFIND", w.ServerBaseURL()+fmt.Sprintf("/webdav/%s", baseName), nil)
	if err != nil {
		t.Fatalf("unable to create PROPFIND request: %v", err)
	}
	r, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("unable to get propfind response: %v", err)
	}
	defer r.Body.Close()

	if r.StatusCode != http.StatusMultiStatus {
		t.Fatalf("unable to get propfind response: got status %v", r.Status)
	}
	b, err = ioutil.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("unable to read webdav response: %v", err)
	}
	// TODO: check the actual response, for now just check that its xml, better than nothing
	if err = xml.Unmarshal(b, &struct{}{}); err != nil {
		t.Fatalf("unable to unmarshal propfind response: %v", err)
	}
}
