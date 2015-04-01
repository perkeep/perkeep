/*
Copyright 2015 The Camlistore Authors

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

// Command dock builds Camlistore's various Docker images.
package main

import (
	"flag"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"camlistore.org/pkg/osutil"
)

var (
	rev = flag.String("rev", "4e8413c5012c", "Camlistore revision to build (tag or commit hash")
)

func main() {
	flag.Parse()
	if flag.NArg() != 0 {
		log.Fatalf("Bogus usage. dock does not currently take any arguments.")
	}

	camDir, err := osutil.GoPackagePath("camlistore.org")
	if err != nil {
		log.Fatalf("Error looking up camlistore.org dir: %v", err)
	}
	dockDir := filepath.Join(camDir, "misc", "docker")

	cmd := exec.Command("docker", "build", "-t", "camlistore/go", ".")
	cmd.Dir = filepath.Join(dockDir, "go")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("Error building camlistore/go: %v", err)
	}

	// TODO(mpl): build djpeg-static here, like camlistore/go
	// above (and prefix the -t value with "camlistore/")
	// (And pull above into a func)

	repl := strings.NewReplacer(
		"[[REV]]", *rev,
	)

	// ctxDir is where we run "docker build" to produce the final
	// "FROM scratch" Docker image.
	ctxDir, err := ioutil.TempDir("", "camli-build")
	if err != nil {
		log.Fatal(err)
	}
	check(os.Mkdir(filepath.Join(ctxDir, "/camlistore.org"), 0755))
	defer os.RemoveAll(ctxDir)

	cmd = exec.Command("docker", "run",
		"--rm",
		"--volume="+ctxDir+"/camlistore.org:/OUT",
		"camlistore/go", "/bin/bash", "-c", repl.Replace(`

# TODO(bradfitz,mpl): rewrite this shell into a Go program that's
# baked into the camlistore/go image, and then all this shell becomes:
# /usr/local/bin/build-camlistore-server $REV
# (and it would still write to /OUT)

     set -e
     set -x
     export GOPATH=/gopath;
     export PATH=/usr/local/go/bin:$PATH;
     mkdir -p /OUT/bin && 
     mkdir -p /OUT/server/camlistored && 
     mkdir -p /gopath/src/camlistore.org && 
     cd /gopath/src/camlistore.org &&
     curl --silent https://camlistore.googlesource.com/camlistore/+archive/[[REV]].tar.gz |
           tar -zxv &&
     CGO_ENABLED=0 go build -o /OUT/bin/camlistored -x --ldflags="-w -d -linkmode internal" --tags=netgo camlistore.org/server/camlistored &&
     mv /gopath/src/camlistore.org/server/camlistored/ui /OUT/server/camlistored/ui &&
     find /gopath/src/camlistore.org/third_party -type f -name '*.go' -exec rm {} \; &&
     mv /gopath/src/camlistore.org/third_party /OUT/third_party &&
     echo DONE
`))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("Error building camlistored in go container: %v", err)
	}

	// Copy Dockerfile into the temp dir.
	serverDockerFile, err := ioutil.ReadFile(filepath.Join(dockDir, "server", "Dockerfile"))
	check(err)
	check(ioutil.WriteFile(filepath.Join(ctxDir, "Dockerfile"), serverDockerFile, 0644))

	// TODO(mpl): copy the djpeg out
	cmd = exec.Command("docker", "build", "-t", "camlistore/server", ".")
	cmd.Dir = ctxDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("Error building camlistore/server: %v", err)
	}
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
