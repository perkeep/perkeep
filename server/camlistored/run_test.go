/*
Copyright 2013 The Camlistore Authors

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
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"camlistore.org/pkg/osutil"
)

func TestStarts(t *testing.T) {
	td, err := ioutil.TempDir("", "camlistored-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(td)

	fakeHome := filepath.Join(td, "fakeHome")
	confDir := filepath.Join(fakeHome, "conf")
	varDir := filepath.Join(fakeHome, "var")

	defer pushEnv("CAMLI_CONFIG_DIR", confDir)()
	defer pushEnv("CAMLI_VAR_DIR", varDir)()

	if _, err := os.Stat(osutil.CamliConfigDir()); !os.IsNotExist(err) {
		t.Fatalf("expected conf dir %q to not exist", osutil.CamliConfigDir())
	}
	if !strings.Contains(osutil.CamliBlobRoot(), td) {
		t.Fatalf("blob root %q should contain the temp dir %q", osutil.CamliBlobRoot(), td)
	}
	if _, err := os.Stat(osutil.CamliBlobRoot()); !os.IsNotExist(err) {
		t.Fatalf("expected blobroot dir %q to not exist", osutil.CamliBlobRoot())
	}
	if fi, err := os.Stat(osutil.UserServerConfigPath()); !os.IsNotExist(err) {
		t.Errorf("expected no server config file; got %v, %v", fi, err)
	}

	mkdir(t, confDir)
	*flagOpenBrowser = false
	*flagListen = ":0"

	up := make(chan struct{})
	down := make(chan struct{})
	dead := make(chan int, 1)
	osExit = func(status int) {
		dead <- status
		close(dead)
		runtime.Goexit()
	}
	go Main(up, down)
	select {
	case status := <-dead:
		t.Errorf("os.Exit(%d) before server came up", status)
		return
	case <-up:
		t.Logf("server is up")
	case <-time.After(10 * time.Second):
		t.Fatal("timeout starting server")
	}

	if _, err := os.Stat(osutil.UserServerConfigPath()); err != nil {
		t.Errorf("expected a server config file; got %v", err)
	}

	down <- struct{}{}
	<-dead
}

func pushEnv(k, v string) func() {
	old := os.Getenv(k)
	os.Setenv(k, v)
	return func() {
		os.Setenv(k, old)
	}
}

func mkdir(t *testing.T, dir string) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
}
