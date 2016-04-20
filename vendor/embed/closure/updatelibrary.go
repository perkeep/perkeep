/*
Copyright 2013 The Camlistore Authors.

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

// The updatelibrary command allows to selectively download
// from the closure library git repository (at a chosen revision)
// the resources needed by the Camlistore ui.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"camlistore.org/pkg/misc/closure"
	"camlistore.org/pkg/osutil"
)

const (
	gitRepo = "https://code.google.com/p/closure-library/"
	gitHash = "ab89cf45c216"
)

var (
	currentRevCmd  = newCmd("git", "rev-parse", "--short", "HEAD")
	gitFetchCmd    = newCmd("git", "fetch")
	gitResetCmd    = newCmd("git", "reset", gitHash)
	gitCloneCmd    = newCmd("git", "clone", "-n", gitRepo, ".")
	gitCheckoutCmd = newCmd("git", "checkout", "HEAD")
)

var (
	verbose       bool
	closureGitDir string // where we do the cloning/updating: camliRoot + tmp/closure-lib/
	destDir       string // install dir: camliRoot + third_party/closure/lib/
)

func init() {
	flag.BoolVar(&verbose, "verbose", false, "verbose output")
}

// fileList parses deps.js from the closure repo, as well as the similar
// dependencies generated for the UI js files, and compiles the list of
// js files from the closure lib required for the UI.
func fileList() ([]string, error) {
	camliRootPath, err := osutil.GoPackagePath("camlistore.org")
	if err != nil {
		log.Fatal("Package camlistore.org not found in $GOPATH (or $GOPATH not defined).")
	}
	uiDir := filepath.Join(camliRootPath, "server", "camlistored", "ui")
	closureDepsFile := filepath.Join(closureGitDir, "closure", "goog", "deps.js")

	f, err := os.Open(closureDepsFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	allClosureDeps, err := closure.DeepParseDeps(f)
	if err != nil {
		return nil, err
	}

	uiDeps, err := closure.GenDeps(http.Dir(uiDir))
	if err != nil {
		return nil, err
	}
	_, requ, err := closure.ParseDeps(bytes.NewReader(uiDeps))
	if err != nil {
		return nil, err
	}

	nameDone := make(map[string]bool)
	jsfilesDone := make(map[string]bool)
	for _, deps := range requ {
		for _, dep := range deps {
			if _, ok := nameDone[dep]; ok {
				continue
			}
			jsfiles := allClosureDeps[dep]
			for _, filename := range jsfiles {
				if _, ok := jsfilesDone[filename]; ok {
					continue
				}
				jsfilesDone[filename] = true
			}
			nameDone[dep] = true
		}
	}
	jsfiles := []string{
		"AUTHORS",
		"LICENSE",
		"README",
		filepath.Join("closure", "goog", "base.js"),
		filepath.Join("closure", "goog", "bootstrap", "nodejs.js"),
		filepath.Join("closure", "goog", "bootstrap", "webworkers.js"),
		filepath.Join("closure", "goog", "css", "common.css"),
		filepath.Join("closure", "goog", "css", "toolbar.css"),
		filepath.Join("closure", "goog", "deps.js"),
	}
	prefix := filepath.Join("closure", "goog")
	for k, _ := range jsfilesDone {
		jsfiles = append(jsfiles, filepath.Join(prefix, k))
	}
	sort.Strings(jsfiles)
	return jsfiles, nil
}

type command struct {
	program string
	args    []string
}

func newCmd(program string, args ...string) *command {
	return &command{program, args}
}

func (c *command) String() string {
	return fmt.Sprintf("%v %v", c.program, c.args)
}

// run runs the command and returns the output if it succeeds.
// On error, the process dies.
func (c *command) run() []byte {
	cmd := exec.Command(c.program, c.args...)
	b, err := cmd.Output()
	if err != nil {
		log.Fatalf("Could not run %v: %v", c, err)
	}
	return b
}

func resetAndCheckout() {
	gitResetCmd.run()
	// we need deps.js to build the list of files, so we get it first
	args := gitCheckoutCmd.args
	args = append(args, filepath.Join("closure", "goog", "deps.js"))
	depsCheckoutCmd := newCmd(gitCheckoutCmd.program, args...)
	depsCheckoutCmd.run()
	files, err := fileList()
	if err != nil {
		log.Fatalf("Could not generate files list: %v", err)
	}
	args = gitCheckoutCmd.args
	args = append(args, files...)
	partialCheckoutCmd := newCmd(gitCheckoutCmd.program, args...)
	if verbose {
		fmt.Printf("%v\n", partialCheckoutCmd)
	}
	partialCheckoutCmd.run()
}

func update() {
	err := os.Chdir(closureGitDir)
	if err != nil {
		log.Fatalf("Could not chdir to %v: %v", closureGitDir, err)
	}
	output := strings.TrimSpace(string(currentRevCmd.run()))
	if string(output) != gitHash {
		gitFetchCmd.run()
	} else {
		if verbose {
			log.Printf("Already at rev %v, fetching not needed.", gitHash)
		}
	}
	resetAndCheckout()
}

func clone() {
	err := os.Chdir(closureGitDir)
	if err != nil {
		log.Fatalf("Could not chdir to %v: %v", closureGitDir, err)
	}
	gitCloneCmd.run()
	resetAndCheckout()
}

func cpDir(src, dst string) error {
	return filepath.Walk(src, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		suffix, err := filepath.Rel(closureGitDir, path)
		if err != nil {
			return fmt.Errorf("Failed to find Rel(%q, %q): %v", closureGitDir, path, err)
		}
		base := fi.Name()
		if fi.IsDir() {
			if base == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		return cpFile(path, filepath.Join(dst, suffix))
	})
}

func cpFile(src, dst string) error {
	sfi, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !sfi.Mode().IsRegular() {
		return fmt.Errorf("cpFile can't deal with non-regular file %s", src)
	}

	dstDir := filepath.Dir(dst)
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return err
	}

	df, err := os.Create(dst)
	if err != nil {
		return err
	}
	sf, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sf.Close()

	n, err := io.Copy(df, sf)
	if err == nil && n != sfi.Size() {
		err = fmt.Errorf("copied wrong size for %s -> %s: copied %d; want %d", src, dst, n, sfi.Size())
	}
	cerr := df.Close()
	if err == nil {
		err = cerr
	}
	return err
}

func cpToDestDir() {
	err := os.RemoveAll(destDir)
	if err != nil {
		log.Fatalf("could not remove %v: %v", destDir, err)
	}
	err = cpDir(closureGitDir, destDir)
	if err != nil {
		log.Fatalf("could not cp %v to %v : %v", closureGitDir, destDir, err)
	}
}

// setup checks if the camlistore root can be found,
// then sets up closureGitDir and destDir, and returns whether
// we should clone or update in closureGitDir (depending on
// if a .git dir was found).
func setup() string {
	camliRootPath, err := osutil.GoPackagePath("camlistore.org")
	if err != nil {
		log.Fatal("Package camlistore.org not found in $GOPATH (or $GOPATH not defined).")
	}
	destDir = filepath.Join(camliRootPath, "third_party", "closure", "lib")
	closureGitDir = filepath.Join(camliRootPath, "tmp", "closure-lib")
	op := "update"
	_, err = os.Stat(closureGitDir)
	if err != nil {
		if os.IsNotExist(err) {
			err = os.MkdirAll(closureGitDir, 0755)
			if err != nil {
				log.Fatalf("Could not create %v: %v", closureGitDir, err)
			}
			op = "clone"
		} else {
			log.Fatalf("Could not stat %v: %v", closureGitDir, err)
		}
	}
	dotGitPath := filepath.Join(closureGitDir, ".git")
	_, err = os.Stat(dotGitPath)
	if err != nil {
		if os.IsNotExist(err) {
			op = "clone"
		} else {
			log.Fatalf("Could not stat %v: %v", dotGitPath, err)
		}
	}
	return op
}

func main() {
	flag.Parse()

	op := setup()
	switch op {
	case "clone":
		if verbose {
			fmt.Printf("cloning from %v at rev %v\n", gitRepo, gitHash)
		}
		clone()
	case "update":
		if verbose {
			fmt.Printf("updating to rev %v\n", gitHash)
		}
		update()
	default:
		log.Fatalf("Unsupported operation: %v", op)
	}

	cpToDestDir()
}
