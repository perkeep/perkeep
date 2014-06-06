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

// Package jstest uses the Go testing package to test JavaScript code using Node and Mocha.
package jstest

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"camlistore.org/pkg/misc/closure"
)

// checkSystemRequirements checks whether system dependencies such as node and npm are present.
func checkSystemRequirements() error {
	binaries := []string{"mocha", "node", "npm"}
	for _, b := range binaries {
		if _, err := exec.LookPath(b); err != nil {
			return fmt.Errorf("Required dependency %q not present", b)
		}
	}

	checkModules := func(globally bool) error {
		args := []string{"list", "--depth=0"}
		if globally {
			args = append([]string{"-g"}, args...)
		}
		c := exec.Command("npm", args...)
		b, _ := c.Output()
		s := string(b)
		modules := []string{"mocha", "assert"}
		for _, m := range modules {
			if !strings.Contains(s, fmt.Sprintf(" %s@", m)) {
				return fmt.Errorf("Required npm module %v not present", m)
			}
		}
		return nil
	}
	if err := checkModules(true); err != nil {
		if err := checkModules(false); err != nil {
			return err
		}
	}
	return nil
}

func getRepoRoot(target string) (string, error) {
	dir, err := filepath.Abs(filepath.Dir(target))
	if err != nil {
		return "", fmt.Errorf("Could not get working directory: %v", err)
	}
	for ; dir != "" && filepath.Base(dir) != "camlistore.org"; dir = filepath.Dir(dir) {
	}
	if dir == "" {
		return "", fmt.Errorf("Could not find Camlistore repo in ancestors of %q", target)
	}
	return dir, nil
}

// writeDeps runs closure.GenDeps() on targetDir and writes the resulting dependencies to a temporary file which will be used during the test run. The entries in the deps files are generated with paths relative to baseJS, which should be Closure's base.js file.
func writeDeps(baseJS, targetDir string) (string, error) {
	closureBaseDir := filepath.Dir(baseJS)
	depPrefix, err := filepath.Rel(closureBaseDir, targetDir)
	if err != nil {
		return "", fmt.Errorf("Could not compute relative path from %q to %q: %v", baseJS, targetDir, err)
	}

	depPrefix += string(os.PathSeparator)
	b, err := closure.GenDepsWithPath(depPrefix, http.Dir(targetDir))
	if err != nil {
		return "", fmt.Errorf("GenDepsWithPath failed: %v", err)
	}
	depsFile, err := ioutil.TempFile("", "camlistore_closure_test_runner")
	if err != nil {
		return "", fmt.Errorf("Could not create temp js deps file: %v", err)
	}
	err = ioutil.WriteFile(depsFile.Name(), b, 0644)
	if err != nil {
		return "", fmt.Errorf("Could not write js deps file: %v", err)
	}
	return depsFile.Name(), nil
}

// TestCwd runs all the tests in the current working directory.
func TestCwd(t *testing.T) {
	err := checkSystemRequirements()
	if err != nil {
		t.Logf("WARNING: JavaScript unit tests could not be run due to a missing system dependency: %v.\nIf you are doing something that might affect JavaScript, you might want to fix this.", err)
		t.Log(err)
		t.Skip()
	}

	path, err := os.Getwd()
	if err != nil {
		t.Fatalf("Could not determine current directory: %v.", err)
	}

	repoRoot, err := getRepoRoot(path)
	if err != nil {
		t.Fatalf("Could not find repository root: %v", err)
	}
	baseJS := filepath.Join(repoRoot, "third_party", "closure", "lib", "closure", "goog", "base.js")
	bootstrap := filepath.Join(filepath.Dir(baseJS), "bootstrap", "nodejs.js")
	depsFile, err := writeDeps(baseJS, path)
	if err != nil {
		t.Fatal(err)
	}

	c := exec.Command("mocha", "-r", bootstrap, "-r", depsFile, filepath.Join(path, "*test.js"))
	b, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf(string(b))
	}
}
