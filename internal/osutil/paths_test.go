/*
Copyright 2011 The Perkeep Authors

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

package osutil

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Creates a file with the content "test" at path
func createTestInclude(path string) error {
	// Create a config file for OpenCamliInclude to play with
	cf, e := os.Create(path)
	if e != nil {
		return e
	}
	fmt.Fprintf(cf, "test")
	return cf.Close()
}

func findCamliInclude(configFile string) (path string, err error) {
	return NewJSONConfigParser().ConfigFilePath(configFile)
}

// Calls OpenCamliInclude to open path, and checks that it contains "test"
func checkOpen(t *testing.T, path string) {
	found, e := findCamliInclude(path)
	if e != nil {
		t.Errorf("Failed to find %v", path)
		return
	}
	var file *os.File
	file, e = os.Open(found)
	if e != nil {
		t.Errorf("Failed to open %v", path)
	} else {
		var d [10]byte
		if n, _ := file.Read(d[:]); n != 4 {
			t.Errorf("Read incorrect number of chars from test.config, wrong file?")
		}
		if string(d[0:4]) != "test" {
			t.Errorf("Wrong test file content: %v", string(d[0:4]))
		}
		file.Close()
	}
}

// Test for error when file doesn't exist
func TestOpenCamliIncludeNoFile(t *testing.T) {
	// Test that error occurs if no such file
	const notExist = "this_config_doesnt_exist.config"

	defer os.Setenv("CAMLI_CONFIG_DIR", os.Getenv("CAMLI_CONFIG_DIR"))
	os.Setenv("CAMLI_CONFIG_DIR", filepath.Join(os.TempDir(), "/x/y/z/not-exist"))

	_, e := findCamliInclude(notExist)
	if e == nil {
		t.Errorf("Successfully opened config which doesn't exist: %v", notExist)
	}
}

// Test for when a file exists in CWD
func TestOpenCamliIncludeCWD(t *testing.T) {
	const path string = "TestOpenCamliIncludeCWD.config"
	if e := createTestInclude(path); e != nil {
		t.Errorf("Couldn't create test config file, aborting test: %v", e)
		return
	}
	defer os.Remove(path)

	// Setting CAMLI_CONFIG_DIR just to avoid triggering failInTests in CamliConfigDir
	defer os.Setenv("CAMLI_CONFIG_DIR", os.Getenv("CAMLI_CONFIG_DIR"))
	os.Setenv("CAMLI_CONFIG_DIR", "whatever") // Restore after test
	checkOpen(t, path)
}

func tempDir(t *testing.T) (path string, cleanup func()) {
	dir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatalf("making tempdir: %v", err)
	}
	return dir, func() { os.RemoveAll(dir) }
}

// Test for when a file exists in CAMLI_CONFIG_DIR
func TestOpenCamliIncludeDir(t *testing.T) {
	td, clean := tempDir(t)
	defer clean()

	const name string = "TestOpenCamliIncludeDir.config"
	if e := createTestInclude(filepath.Join(td, name)); e != nil {
		t.Errorf("Couldn't create test config file, aborting test: %v", e)
		return
	}
	os.Setenv("CAMLI_CONFIG_DIR", td)
	defer os.Setenv("CAMLI_CONFIG_DIR", "")

	checkOpen(t, name)
}

// Test for when a file exits in CAMLI_INCLUDE_PATH
func TestOpenCamliIncludePath(t *testing.T) {
	td, clean := tempDir(t)
	defer clean()

	const name string = "TestOpenCamliIncludePath.config"
	if e := createTestInclude(filepath.Join(td, name)); e != nil {
		t.Errorf("Couldn't create test config file, aborting test: %v", e)
		return
	}
	defer os.Setenv("CAMLI_INCLUDE_PATH", "")

	defer os.Setenv("CAMLI_CONFIG_DIR", os.Getenv("CAMLI_CONFIG_DIR"))
	os.Setenv("CAMLI_CONFIG_DIR", filepath.Join(td, "/x/y/z/not-exist"))

	os.Setenv("CAMLI_INCLUDE_PATH", td)
	checkOpen(t, name)

	const sep = string(filepath.ListSeparator)
	os.Setenv("CAMLI_INCLUDE_PATH", "/not/a/camli/config/dir"+sep+td)
	checkOpen(t, name)

	os.Setenv("CAMLI_INCLUDE_PATH", "/not/a/camli/config/dir"+sep+td+sep+"/another/fake/camli/dir")
	checkOpen(t, name)
}

func TestCamPkConfigMigration(t *testing.T) {
	oldFuncs := configDirFuncs
	defer func() {
		configDirFuncs = oldFuncs
		configDirNamedTestHook = nil
		log.SetOutput(os.Stderr)
	}()
	log.SetOutput(ioutil.Discard)

	td, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(td)

	configDirNamedTestHook = func(name string) string {
		return filepath.Join(td, name)
	}

	oldDir := filepath.Join(td, "camlistore")
	newDir := filepath.Join(td, "perkeep")

	if err := os.MkdirAll(filepath.Join(oldDir, "blobs", "foo", "sub"), 0755); err != nil {
		t.Fatal(err)
	}

	calls := 0
	RegisterConfigDirFunc(func() string {
		calls++
		log.Printf("call %d", calls)
		switch calls {
		case 1:
			return oldDir
		case 2:
			return newDir
		}
		t.Fatalf("unexpected %d calls to get config dir", calls)
		return ""
	})

	got, err := perkeepConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != newDir {
		t.Errorf("first call = %v; want %v", got, newDir)
	}

	if fi, err := os.Lstat(oldDir); !os.IsNotExist(err) {
		t.Errorf("Lstat = %v, %v; want IsNotExist error", fi, err)
	}

	// Now try with some regular file in the old dir.
	if err := os.MkdirAll(filepath.Join(oldDir, "blobs"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := ioutil.WriteFile(filepath.Join(oldDir, "blobs/x.dat"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err = perkeepConfigDir()
	if err == nil {
		t.Error("unexpected success looking up config dir after the old one had a file in it")
	} else if !strings.Contains(err.Error(), "old configuration directory detected") {
		t.Errorf("expected migration error; got: %v", err)
	}

	if fi, err := os.Lstat(oldDir); err != nil || !fi.IsDir() {
		t.Errorf("error looking up old directory; want valid directory. Got: %v, %v", fi, err)
	}
}
