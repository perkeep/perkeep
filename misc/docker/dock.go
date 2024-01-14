/*
Copyright 2015 The Perkeep Authors

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

// Command dock builds Perkeep's various Docker images.
// It can also generate a tarball of the Perkeep server and tools.
package main // import "perkeep.org/misc/docker"

import (
	"compress/gzip"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"perkeep.org/internal/osutil"

	"cloud.google.com/go/storage"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
)

var (
	flagRev    = flag.String("rev", "", "Perkeep revision to build (tag or commit hash). For development purposes, you can instead specify the path to a local Perkeep source tree from which to build, with the form \"WIP:/path/to/dir\".")
	flagUpload = flag.Bool("upload", false, "Whether to pload a snapshot of the server in docker as a tarball to https://storage.googleapis.com/camlistore-release/docker/.")

	asCamlistore = flag.Bool("as_camli", false, `generate and upload things using the old "camlistore" based names. This exists in order to migrate users on the camlistore named image/systemd service, to the new perkeed named ones.`)
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

var (
	dockDir     string
	serverImage = "perkeep/server"
)

const (
	goDockerImage       = "perkeep/go"
	djpegDockerImage    = "perkeep/djpeg"
	zoneinfoDockerImage = "perkeep/zoneinfo"
	goCmd               = "/usr/local/go/bin/go"
	// Path to where the Perkeep builder is mounted on the perkeep/go image.
	genPkProgram = "/usr/local/bin/build-perkeep-server.go"
)

func isWIP() bool {
	return strings.HasPrefix(*flagRev, "WIP")
}

// localCamliSource returns the path to the local Perkeep source tree
// that should be specified in *flagRev if *flagRev starts with "WIP:",
// empty string otherwise.
func localCamliSource() string {
	if !isWIP() {
		return ""
	}
	return strings.TrimPrefix(*flagRev, "WIP:")
}

func rev() string {
	if isWIP() {
		return "WORKINPROGRESS"
	}
	return *flagRev
}

func genPerkeep(ctxDir string) {
	check(os.Mkdir(filepath.Join(ctxDir, "/perkeep.org"), 0755))

	args := []string{
		"run",
		"--rm",
		"--volume=" + ctxDir + "/perkeep.org:/OUT",
		"--volume=" + path.Join(dockDir, "server/build-perkeep-server.go") + ":" + genPkProgram + ":ro",
	}
	if isWIP() {
		args = append(args, "--volume="+localCamliSource()+":/IN:ro",
			goDockerImage, goCmd, "run", genPkProgram, "--rev=WIP:/IN")
	} else {
		args = append(args, goDockerImage, goCmd, "run", genPkProgram, "--rev="+rev())
	}
	cmd := exec.Command("docker", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("Error building perkeepd in go container: %v", err)
	}
}

func copyFinalDockerfile(ctxDir string) {
	// Copy Dockerfile into the temp dir.
	serverDockerFile, err := os.ReadFile(filepath.Join(dockDir, "server", "Dockerfile"))
	check(err)
	check(os.WriteFile(filepath.Join(ctxDir, "Dockerfile"), serverDockerFile, 0644))
}

func genDjpeg(ctxDir string) {
	cmd := exec.Command("docker", "run",
		"--rm",
		"--volume="+ctxDir+":/OUT",
		djpegDockerImage, "/bin/bash", "-c", "mkdir -p /OUT && cp /src/libjpeg-turbo-1.4.1/djpeg /OUT/djpeg")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("Error building djpeg in go container: %v", err)
	}
}

func genZoneinfo(ctxDir string) {
	cmd := exec.Command("docker", "run",
		"--rm",
		"--volume="+ctxDir+":/OUT",
		zoneinfoDockerImage, "/bin/bash", "-c", "mkdir -p /OUT && cp -a /usr/share/zoneinfo /OUT/zoneinfo")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("Error generating zoneinfo in go container: %v", err)
	}
}

func buildServer(ctxDir string) {
	copyFinalDockerfile(ctxDir)
	cmd := exec.Command("docker", "build", "--no-cache", "-t", serverImage, ".")
	cmd.Dir = ctxDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("Error building %v: %v", serverImage, err)
	}
}

func publicACL(proj string) []storage.ACLRule {
	return []storage.ACLRule{
		// If you don't give the owners access, the web UI seems to
		// have a bug and doesn't have access to see that it's public, so
		// won't render the "Shared Publicly" link. So we do that, even
		// though it's dumb and unnecessary otherwise:
		{Entity: storage.ACLEntity("project-owners-" + proj), Role: storage.RoleOwner},
		{Entity: storage.AllUsers, Role: storage.RoleReader},
	}
}

// uploadDockerImage makes a tar.gz snapshot of the perkeepd docker image,
// and uploads it at camlistore-release/docker/perkeepd-REV.tar.gz. It then
// makes a copy in the same bucket and path as perkeepd.tar.gz.
func uploadDockerImage() {
	proj := "camlistore-website"
	bucket := "camlistore-release"
	versionedTarball := "docker/perkeepd-" + rev() + ".tar.gz"
	tarball := "docker/perkeepd.tar.gz"
	versionFile := "docker/VERSION"
	if *asCamlistore {
		versionedTarball = strings.Replace(versionedTarball, "perkeepd", "camlistored", 1)
		tarball = strings.Replace(tarball, "perkeepd", "camlistored", 1)
	}

	log.Printf("Uploading %s/%s ...", bucket, versionedTarball)

	ts, err := tokenSource(bucket)
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	stoClient, err := storage.NewClient(ctx, option.WithTokenSource(ts), option.WithHTTPClient(oauth2.NewClient(ctx, ts)))
	if err != nil {
		log.Fatal(err)
	}
	w := stoClient.Bucket(bucket).Object(versionedTarball).NewWriter(ctx)
	w.ACL = publicACL(proj)
	w.CacheControl = "no-cache" // TODO: remove for non-tip releases? set expirations?
	w.ContentType = "application/x-gtar"

	dockerSave := exec.Command("docker", "save", serverImage)
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
		log.Fatalf("Error starting docker save %v: %v", serverImage, err)
	}
	if _, err := io.Copy(w, targz); err != nil {
		log.Fatalf("io.Copy: %v", err)
	}
	if err := w.Close(); err != nil {
		log.Fatalf("closing GCS storage writer: %v", err)
	}
	if err := dockerSave.Wait(); err != nil {
		log.Fatalf("Error waiting for docker save %v: %v", serverImage, err)
	}
	log.Printf("Uploaded tarball to %s", versionedTarball)
	if isWIP() {
		return
	}
	log.Printf("Copying tarball to %s/%s ...", bucket, tarball)
	dest := stoClient.Bucket(bucket).Object(tarball)
	cpier := dest.CopierFrom(stoClient.Bucket(bucket).Object(versionedTarball))
	cpier.ObjectAttrs = storage.ObjectAttrs{
		ACL:          publicACL(proj),
		CacheControl: "no-cache",
		ContentType:  "application/x-gtar",
	}
	if _, err := cpier.Run(ctx); err != nil {
		log.Fatalf("Error uploading %v: %v", tarball, err)
	}
	log.Printf("Uploaded tarball to %s", tarball)

	log.Printf("Updating %s/%s file...", bucket, versionFile)
	w = stoClient.Bucket(bucket).Object(versionFile).NewWriter(ctx)
	w.ACL = publicACL(proj)
	w.CacheControl = "no-cache"
	w.ContentType = "text/plain"
	if _, err := io.Copy(w, strings.NewReader(rev())); err != nil {
		log.Fatalf("io.Copy: %v", err)
	}
	if err := w.Close(); err != nil {
		log.Fatalf("closing GCS storage writer: %v", err)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage:\n")
	fmt.Fprintf(os.Stderr, "%s [-rev perkeep_revision | -rev WIP:/path/to/perkeep/source]\n", os.Args[0])
	flag.PrintDefaults()
	os.Exit(1)
}

func checkFlags() {
	if flag.NArg() != 0 {
		usage()
	}
	if *flagRev == "" {
		fmt.Fprintf(os.Stderr, "Usage error: --rev is required.\n")
		usage()
	}
	if isWIP() {
		if _, err := os.Stat(localCamliSource()); err != nil {
			fmt.Fprintf(os.Stderr, "Usage error: could not stat path %q provided with --rev: %v", localCamliSource(), err)
			usage()
		}
	}
}

func main() {
	flag.Usage = usage
	flag.Parse()
	checkFlags()

	srcRoot, err := osutil.PkSourceRoot()
	if err != nil {
		log.Fatalf("Error looking up perkeep.org dir: %v", err)
	}
	dockDir = filepath.Join(srcRoot, "misc", "docker")

	if *asCamlistore {
		serverImage = "camlistore/server"
	}

	buildDockerImage("go", goDockerImage)
	// ctxDir is where we run "docker build" to produce the final
	// "FROM scratch" Docker image.
	ctxDir, err := os.MkdirTemp("", "pk-build_docker_image")
	if err != nil {
		log.Fatal(err)
	}
	// Docker Desktop for Mac by default shares /private, but does not share /var.
	// os.MkdirTemp gives us something in /var/folders, but apparently the same
	// location prefixed with /private is equivalent, so it all works out if we use
	// everywhere the path prefixed with /private.
	if runtime.GOOS == "darwin" {
		ctxDir = "/private" + ctxDir
	}
	defer os.RemoveAll(ctxDir)

	buildDockerImage("djpeg-static", djpegDockerImage)
	buildDockerImage("zoneinfo", zoneinfoDockerImage)
	genPerkeep(ctxDir)
	genDjpeg(ctxDir)
	genZoneinfo(ctxDir)
	buildServer(ctxDir)

	if !*flagUpload {
		return
	}
	uploadDockerImage()
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
	jsonConf, err := os.ReadFile(fileName)
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
	return conf.TokenSource(context.TODO()), nil
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
