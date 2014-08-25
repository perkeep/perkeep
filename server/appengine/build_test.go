/*
Copyright 2013 Google Inc.

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

package appengine_test

import (
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"camlistore.org/pkg/osutil"
)

func TestAppEngineBuilds(t *testing.T) {
	t.Skip("Currently broken until App Engine supports Go 1.3")
	if runtime.GOOS == "windows" {
		t.Skip("skipping on Windows; don't want to deal with escaping backslashes")
	}
	camRoot, err := osutil.GoPackagePath("camlistore.org")
	if err != nil {
		t.Errorf("No camlistore.org package in GOPATH: %v", err)
	}
	sdkLink := filepath.Join(camRoot, "appengine-sdk")
	if _, err := os.Lstat(sdkLink); os.IsNotExist(err) {
		t.Skipf("Skipping test; no App Engine SDK symlink at %s pointing to App Engine SDK.", sdkLink)
	}
	sdk, err := os.Readlink(sdkLink)
	if err != nil {
		t.Fatal(err)
	}

	td, err := ioutil.TempDir("", "camli-appengine")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(td)

	gab := filepath.Join(sdk, "goroot", "bin", "go-app-builder")
	if runtime.GOOS == "windows" {
		gab += ".exe"
	}

	appBase := filepath.Join(camRoot, "server", "appengine")
	f, err := os.Open(filepath.Join(appBase, "camli"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	srcFilesAll, err := f.Readdirnames(-1)
	if err != nil {
		t.Fatal(err)
	}

	appenginePkg := filepath.Join(sdk, "goroot", "pkg", runtime.GOOS+"_"+runtime.GOARCH+"_appengine")
	cmd := exec.Command(gab,
		"-app_base", appBase,
		"-arch", archChar(),
		"-binary_name", "_go_app",
		"-dynamic",
		"-extra_imports", "appengine_internal/init",
		"-goroot", filepath.Join(sdk, "goroot"),
		"-gcflags", "-I,"+appenginePkg,
		"-ldflags", "-L,"+appenginePkg,
		"-nobuild_files", "^^$",
		"-unsafe",
		"-work_dir", td,
		"-gopath", os.Getenv("GOPATH"),
		// "-v",
	)
	for _, f := range srcFilesAll {
		if strings.HasSuffix(f, ".go") {
			cmd.Args = append(cmd.Args, filepath.Join("camli", f))
		}
	}
	for _, pair := range os.Environ() {
		if strings.HasPrefix(pair, "GOROOT=") {
			continue
		}
		cmd.Env = append(cmd.Env, pair)
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Error: %v\n%s", err, out)
	}
	target := filepath.Join(td, "_go_app")
	if _, err := os.Stat(target); os.IsNotExist(err) {
		t.Errorf("target binary doesn't exist")
	}
}

func archChar() string {
	switch runtime.GOARCH {
	case "386":
		return "8"
	case "amd64":
		return "6"
	case "arm":
		return "5"
	}
	panic("unknown arch " + runtime.GOARCH)
}
