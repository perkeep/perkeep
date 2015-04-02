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
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"camlistore.org/pkg/osutil"
	"camlistore.org/third_party/golang.org/x/oauth2"
	"camlistore.org/third_party/golang.org/x/oauth2/google"
	"camlistore.org/third_party/google.golang.org/cloud"
	"camlistore.org/third_party/google.golang.org/cloud/storage"
)

var (
	rev = flag.String("rev", "4e8413c5012c", "Camlistore revision to build (tag or commit hash")

	doBuildServer = flag.Bool("build_server", true, "build the server")
	doUpload      = flag.Bool("upload", false, "upload a snapshot of the server tarball to http://storage.googleapis.com/camlistore-release/docker/camlistored[-VERSION].tar.gz")
)

// buildDockerImage builds a docker image from the Dockerfile located in
// imageDir, which is a path relative to dockDir. The image will be named after
// imageName. dockDir should have been set behorehand.
func buildDockerImage(imageDir, imageName string) {
	if dockDir == "" {
		panic("dockDir should be set before calling buildDockerImage")
	}
	cmd := exec.Command("docker", "build", "-t", imageName, ".")
	cmd.Dir = filepath.Join(dockDir, imageDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("Error building docker image %v: %v", imageName, err)
	}
}

var dockDir string

const (
	goDockerImage    = "camlistore/go"
	djpegDockerImage = "camlistore/djpeg"
)

func genCamlistore(ctxDir string) {
	repl := strings.NewReplacer(
		"[[REV]]", *rev,
	)
	check(os.Mkdir(filepath.Join(ctxDir, "/camlistore.org"), 0755))

	cmd := exec.Command("docker", "run",
		"--rm",
		"--volume="+ctxDir+"/camlistore.org:/OUT",
		goDockerImage, "/bin/bash", "-c", repl.Replace(`

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
     CGO_ENABLED=0 go build \
          -o /OUT/bin/camlistored \
          --ldflags="-w -d -linkmode internal -X camlistore.org/pkg/buildinfo.GitInfo [[REV]]" \
          --tags=netgo \
          camlistore.org/server/camlistored &&
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
}

func copyFinalDockerfile(ctxDir string) {
	// Copy Dockerfile into the temp dir.
	serverDockerFile, err := ioutil.ReadFile(filepath.Join(dockDir, "server", "Dockerfile"))
	check(err)
	check(ioutil.WriteFile(filepath.Join(ctxDir, "Dockerfile"), serverDockerFile, 0644))
}

func genDjpeg(ctxDir string) {
	cmd := exec.Command("docker", "run",
		"--rm",
		"--volume="+ctxDir+":/OUT",
		djpegDockerImage, "/bin/bash", "-c", "mkdir -p /OUT && cp /src/libjpeg-turbo-1.4.0/djpeg /OUT/djpeg")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("Error building djpeg in go container: %v", err)
	}
}

func buildServer(ctxDir string) {
	copyFinalDockerfile(ctxDir)
	cmd := exec.Command("docker", "build", "-t", "camlistore/server", ".")
	cmd.Dir = ctxDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("Error building camlistore/server: %v", err)
	}
}

func uploadDockerImage() {
	proj := "camlistore-website"
	bucket := "camlistore-release"
	object := "docker/camlistored.tar.gz" // TODO: this is only tip for now

	log.Printf("Uploading %s/%s ...", bucket, object)

	ts, err := tokenSource(bucket)
	if err != nil {
		log.Fatal(err)
	}

	httpClient := oauth2.NewClient(oauth2.NoContext, ts)
	ctx := cloud.NewContext(proj, httpClient)
	w := storage.NewWriter(ctx, bucket, object)
	// If you don't give the owners access, the web UI seems to
	// have a bug and doesn't have access to see that it's public, so
	// won't render the "Shared Publicly" link. So we do that, even
	// though it's dumb and unnecessary otherwise:
	w.ACL = append(w.ACL, storage.ACLRule{Entity: storage.ACLEntity("project-owners-" + proj), Role: storage.RoleOwner})
	w.ACL = append(w.ACL, storage.ACLRule{Entity: storage.AllUsers, Role: storage.RoleReader})
	w.CacheControl = "no-cache" // TODO: remove for non-tip releases? set expirations?
	w.ContentType = "application/x-gtar"

	dockerSave := exec.Command("docker", "save", "camlistore/server")
	dockerSave.Stderr = os.Stderr
	tar, err := dockerSave.StdoutPipe()
	if err != nil {
		log.Fatal(err)
	}
	targz, pw := io.Pipe()
	go func() {
		zw := gzip.NewWriter(pw)
		n, err := io.Copy(zw, tar)
		if err != nil {
			log.Fatalf("Error copying to gzip writer: after %d bytes, %v", n, err)
		}
		if err := zw.Close(); err != nil {
			log.Fatalf("gzip.Close: %v", err)
		}
		pw.CloseWithError(err)
	}()
	if err := dockerSave.Start(); err != nil {
		log.Fatalf("Error starting docker save camlistore/server: %v", err)
	}
	if _, err := io.Copy(w, targz); err != nil {
		log.Fatalf("io.Copy: %v", err)
	}
	if err := w.Close(); err != nil {
		log.Fatalf("closing GCS storage writer: %v", err)
	}
	if err := dockerSave.Wait(); err != nil {
		log.Fatalf("Error waiting for docker save camlistore/server: %v", err)
	}
	log.Printf("Uploaded tarball to %s", object)
}

func main() {
	flag.Parse()
	if flag.NArg() != 0 {
		log.Fatalf("Bogus usage. dock does not currently take any arguments.")
	}

	camDir, err := osutil.GoPackagePath("camlistore.org")
	if err != nil {
		log.Fatalf("Error looking up camlistore.org dir: %v", err)
	}
	dockDir = filepath.Join(camDir, "misc", "docker")

	if *doBuildServer {
		buildDockerImage("go", goDockerImage)
		buildDockerImage("djpeg-static", djpegDockerImage)

		// ctxDir is where we run "docker build" to produce the final
		// "FROM scratch" Docker image.
		ctxDir, err := ioutil.TempDir("", "camli-build")
		if err != nil {
			log.Fatal(err)
		}
		defer os.RemoveAll(ctxDir)

		genCamlistore(ctxDir)

		genDjpeg(ctxDir)

		buildServer(ctxDir)
	}

	if *doUpload {
		uploadDockerImage()
	}
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func homedir() string {
	if runtime.GOOS == "windows" {
		return os.Getenv("HOMEDRIVE") + os.Getenv("HOMEPATH")
	}
	return os.Getenv("HOME")
}

// ProjectTokenSource returns an OAuth2 TokenSource for the given Google Project ID.
func ProjectTokenSource(proj string, scopes ...string) (oauth2.TokenSource, error) {
	// TODO(bradfitz): try different strategies too, like
	// three-legged flow if the service account doesn't exist, and
	// then cache the token file on disk somewhere. Or maybe that should be an
	// option, for environments without stdin/stdout available to the user.
	// We'll figure it out as needed.
	fileName := filepath.Join(homedir(), "keys", proj+".key.json")
	jsonConf, err := ioutil.ReadFile(fileName)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("Missing JSON key configuration. Download the Service Account JSON key from https://console.developers.google.com/project/%s/apiui/credential and place it at %s", proj, fileName)
		}
		return nil, err
	}
	conf, err := google.JWTConfigFromJSON(jsonConf, scopes...)
	if err != nil {
		return nil, fmt.Errorf("reading JSON config from %s: %v", fileName, err)
	}
	return conf.TokenSource(oauth2.NoContext), nil
}

var bucketProject = map[string]string{
	"camlistore-release": "camlistore-website",
}

func tokenSource(bucket string) (oauth2.TokenSource, error) {
	proj, ok := bucketProject[bucket]
	if !ok {
		return nil, fmt.Errorf("unknown project for bucket %q", bucket)
	}
	return ProjectTokenSource(proj, storage.ScopeReadWrite)
}
