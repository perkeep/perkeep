/*
Copyright 2014 The Camlistore Authors.

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

package integration

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"camlistore.org/pkg/test"
)

func TestFileSharing(t *testing.T) {
	share(t, "share_test.go")
}

func TestDirSharing(t *testing.T) {
	share(t, filepath.FromSlash("../integration"))
}

func share(t *testing.T, file string) {
	w := test.GetWorld(t)
	out := test.MustRunCmd(t, w.Cmd("camput", "file", file))
	fileRef := strings.Split(out, "\n")[0]

	out = test.MustRunCmd(t, w.Cmd("camput", "share", "-transitive", fileRef))
	shareRef := strings.Split(out, "\n")[0]

	testDir, err := ioutil.TempDir("", "camli-share-test-")
	if err != nil {
		t.Fatalf("ioutil.TempDir(): %v", err)
	}
	defer os.RemoveAll(testDir)

	// test that we can get it through the share
	test.MustRunCmd(t, w.Cmd("camget", "-o", testDir, "-shared", fmt.Sprintf("%v/share/%v", w.ServerBaseURL(), shareRef)))
	filePath := filepath.Join(testDir, filepath.Base(file))
	fi, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("camget -shared failed to get %v: %v", file, err)
	}
	if fi.IsDir() {
		// test that we also get the dir contents
		d, err := os.Open(filePath)
		if err != nil {
			t.Fatal(err)
		}
		defer d.Close()
		names, err := d.Readdirnames(-1)
		if err != nil {
			t.Fatal(err)
		}
		if len(names) == 0 {
			t.Fatalf("camget did not fetch contents of directory %v", file)
		}
	}

	// test that we're not allowed to get it directly
	fileURL := fmt.Sprintf("%v/share/%v", w.ServerBaseURL(), fileRef)
	_, err = test.RunCmd(w.Cmd("camget", "-shared", fileURL))
	if err == nil {
		t.Fatal("Was expecting error for 'camget -shared " + fileURL + "'")
	}
	if !strings.Contains(err.Error(), "client: got status code 401") {
		t.Fatalf("'camget -shared %v': got error %v, was expecting 401", fileURL, err)
	}
}
