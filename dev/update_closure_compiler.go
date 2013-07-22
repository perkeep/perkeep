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

// update_closure_compiler downloads a new version
// of the closure compiler if the one in tmp/closure-compiler
// doesn't exist or is older than the requested version.
package main

import (
	"archive/zip"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"

	"camlistore.org/pkg/osutil"
)

const (
	compilerDirURL  = "http://closure-compiler.googlecode.com/files/"
	compilerVersion = "20121212"
)

var rgxVersion = regexp.MustCompile(`.*Version: (.*) \(revision.*`)

func main() {

	// check JRE presence
	_, err := exec.LookPath("java")
	if err != nil {
		log.Fatal("Didn't find 'java' in $PATH. The Java Runtime Environment is needed to run the closure compiler.\n")
	}

	camliRootPath, err := osutil.GoPackagePath("camlistore.org")
	if err != nil {
		log.Fatal("Package camlistore.org not found in $GOPATH (or $GOPATH not defined).")
	}
	destDir := filepath.Join(camliRootPath, "tmp", "closure-compiler")
	// check if compiler already exists
	jarFile := filepath.Join(destDir, "compiler.jar")
	_, err = os.Stat(jarFile)
	if err == nil {
		// if compiler exists, check version
		cmd := exec.Command("java", "-jar", jarFile, "--version", "--help", "2>&1")
		output, _ := cmd.CombinedOutput()
		m := rgxVersion.FindStringSubmatch(string(output))
		if m == nil {
			log.Fatalf("Could not find compiler version in %q", output)
		}
		if m[1] == compilerVersion {
			log.Printf("compiler already at version %v , nothing to do.", compilerVersion)
			os.Exit(0)
		}
		if err := os.Remove(jarFile); err != nil {
			log.Fatalf("Could not remove %v: %v", jarFile, err)
		}
	} else {
		if !os.IsNotExist(err) {
			log.Fatalf("Could not stat %v: %v", jarFile, err)
		}
	}

	// otherwise, download compiler
	log.Printf("Getting closure compiler version %s.\n", compilerVersion)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		log.Fatal(err)
	}
	if err := os.Chdir(destDir); err != nil {
		log.Fatal(err)
	}
	zipFilename := "compiler-" + compilerVersion + ".zip"
	compilerURL := compilerDirURL + zipFilename
	resp, err := http.Get(compilerURL)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	f, err := os.Create(zipFilename)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		log.Fatal(err)
	}
	if err := f.Close(); err != nil {
		log.Fatal(err)
	}

	r, err := zip.OpenReader(zipFilename)
	if err != nil {
		log.Fatal(err)
	}
	for x, f := range r.File {
		if f.FileHeader.Name != "compiler.jar" {
			if x == len(r.File)-1 {
				log.Fatal("compiler.jar was not found in the zip archive")
			}
			continue
		}
		rc, err := f.Open()
		if err != nil {
			log.Fatal(err)
		}
		g, err := os.Create(jarFile)
		if err != nil {
			log.Fatal(err)
		}
		defer g.Close()
		if _, err = io.Copy(g, rc); err != nil {
			log.Fatal(err)
		}
		rc.Close()
		break
	}

	if err := r.Close(); err != nil {
		log.Fatal(err)
	}
	if err := os.Remove(zipFilename); err != nil {
		log.Fatal(err)
	}
	log.Printf("Success. Installed at %v", jarFile)
}
