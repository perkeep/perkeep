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
// It can also generate a tarball of the Camlistore server and tools.
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"

	"camlistore.org/pkg/osutil"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/cloud"
	"google.golang.org/cloud/storage"
)

var (
	rev      = flag.String("rev", "5ffc23ebd13c", "Camlistore revision to build (tag or commit hash)")
	localSrc = flag.String("camlisource", "", "(dev flag) Path to a local Camlistore source tree from which to build. This flag is ignored unless -rev=WORKINPROGRESS")
	buildOS  = flag.String("os", runtime.GOOS, "Operating system to build for. Requires --build_binaries.")

	doImage    = flag.Bool("build_image", true, "build the Camlistore server as a docker image")
	doUpload   = flag.Bool("upload", false, "upload a snapshot of the server tarball to http://storage.googleapis.com/camlistore-release/docker/camlistored[-VERSION].tar.gz")
	doBinaries = flag.Bool("build_binaries", false, "build the Camlistore server and tools as standalone binaries to a tarball in misc/docker/release. Requires --build_image=false.")
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
	serverImage      = "camlistore/server"
	goCmd            = "/usr/local/go/bin/go"
	// Path to where the Camlistore builder is mounted on the camlistore/go image.
	genCamliProgram    = "/usr/local/bin/build-camlistore-server.go"
	genBinariesProgram = "/usr/local/bin/build-binaries.go"
)

func genCamlistore(ctxDir string) {
	check(os.Mkdir(filepath.Join(ctxDir, "/camlistore.org"), 0755))

	args := []string{
		"run",
		"--rm",
		"--volume=" + ctxDir + "/camlistore.org:/OUT",
		"--volume=" + path.Join(dockDir, "server/build-camlistore-server.go") + ":" + genCamliProgram + ":ro",
	}
	if *rev == "WORKINPROGRESS" {
		args = append(args, "--volume="+*localSrc+":/IN:ro",
			goDockerImage, goCmd, "run", genCamliProgram, "--rev="+*rev, "--camlisource=/IN")
	} else {
		args = append(args, goDockerImage, goCmd, "run", genCamliProgram, "--rev="+*rev)
	}
	cmd := exec.Command("docker", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("Error building camlistored in go container: %v", err)
	}
}

func genBinaries(ctxDir string) {
	check(os.Mkdir(filepath.Join(ctxDir, "/camlistore.org"), 0755))
	image := goDockerImage
	args := []string{
		"run",
		"--rm",
		"--volume=" + ctxDir + "/camlistore.org:/OUT",
		"--volume=" + path.Join(dockDir, "release/build-binaries.go") + ":" + genBinariesProgram + ":ro",
	}
	if *rev == "WORKINPROGRESS" {
		args = append(args, "--volume="+*localSrc+":/IN:ro",
			image, goCmd, "run", genBinariesProgram, "--rev="+*rev, "--camlisource=/IN", "--os="+*buildOS)
	} else {
		args = append(args, image, goCmd, "run", genBinariesProgram, "--rev="+*rev, "--os="+*buildOS)
	}
	cmd := exec.Command("docker", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("Error building binaries in go container: %v", err)
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
		djpegDockerImage, "/bin/bash", "-c", "mkdir -p /OUT && cp /src/libjpeg-turbo-1.4.1/djpeg /OUT/djpeg")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("Error building djpeg in go container: %v", err)
	}
}

func buildServer(ctxDir string) {
	copyFinalDockerfile(ctxDir)
	cmd := exec.Command("docker", "build", "-t", serverImage, ".")
	cmd.Dir = ctxDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("Error building %v: %v", serverImage, err)
	}
}

func uploadDockerImage() {
	proj := "camlistore-website"
	bucket := "camlistore-release"
	versionedTarball := "docker/camlistored-" + *rev + ".tar.gz"
	tarball := "docker/camlistored.tar.gz"

	log.Printf("Uploading %s/%s ...", bucket, versionedTarball)

	ts, err := tokenSource(bucket)
	if err != nil {
		log.Fatal(err)
	}

	httpClient := oauth2.NewClient(oauth2.NoContext, ts)
	ctx := cloud.NewContext(proj, httpClient)
	w := storage.NewWriter(ctx, bucket, versionedTarball)
	// If you don't give the owners access, the web UI seems to
	// have a bug and doesn't have access to see that it's public, so
	// won't render the "Shared Publicly" link. So we do that, even
	// though it's dumb and unnecessary otherwise:
	acl := append(w.ACL, storage.ACLRule{Entity: storage.ACLEntity("project-owners-" + proj), Role: storage.RoleOwner})
	acl = append(acl, storage.ACLRule{Entity: storage.AllUsers, Role: storage.RoleReader})
	w.ACL = acl
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
	log.Printf("Copying tarball to %s/%s ...", bucket, tarball)
	if _, err := storage.CopyObject(ctx, bucket, versionedTarball, bucket, tarball, nil); err != nil {
		log.Fatalf("Error uploading %v: %v", tarball, err)
	}
	log.Printf("Uploaded tarball to %s", tarball)
}

func exeName(s string) string {
	if *buildOS == "windows" {
		return s + ".exe"
	}
	return s
}

func packBinaries(ctxDir string) {
	binaries := map[string]bool{
		exeName("camlistored"): false,
		exeName("camget"):      false,
		exeName("camput"):      false,
		exeName("camtool"):     false,
		exeName("publisher"):   false,
	}
	switch *buildOS {
	case "linux", "darwin":
		binaries["cammount"] = false
	}
	toPack := func(bin string) bool {
		for k, _ := range binaries {
			if bin == k {
				binaries[k] = true
				return true
			}
		}
		return false
	}
	defer func() {
		for name, found := range binaries {
			if !found {
				log.Fatalf("%v was not packed in tarball", name)
			}
		}
	}()

	binDir := path.Join(ctxDir, "camlistore.org", "bin")
	check(os.Chdir(binDir))
	dir, err := os.Open(binDir)
	check(err)
	defer dir.Close()

	if *buildOS == "windows" {
		fw, err := os.Create(path.Join(dockDir, "release", "camlistore-"+*buildOS+".zip"))
		check(err)
		defer func() {
			check(fw.Close())
		}()
		w := zip.NewWriter(fw)
		defer func() {
			check(w.Close())
		}()
		names, err := dir.Readdirnames(-1)
		check(err)
		for _, name := range names {
			if !toPack(name) {
				continue
			}
			b, err := ioutil.ReadFile(path.Join(binDir, name))
			check(err)
			f, err := w.Create(name)
			check(err)
			_, err = f.Write(b)
			check(err)
		}
		return
	}

	fw, err := os.Create(path.Join(dockDir, "release", "camlistore-"+*buildOS+".tar.gz"))
	check(err)
	defer func() {
		check(fw.Close())
	}()
	pr, pw := io.Pipe()
	go func() {
		tw := tar.NewWriter(pw)
		fis, err := dir.Readdir(-1)
		check(err)
		for _, file := range fis {
			if !toPack(file.Name()) {
				continue
			}
			hdr, err := tar.FileInfoHeader(file, "")
			check(err)
			check(tw.WriteHeader(hdr))
			fr, err := os.Open(file.Name())
			check(err)
			n, err := io.Copy(tw, fr)
			check(err)
			fr.Close()
			if n != file.Size() {
				log.Fatalf("failed to tar all of %v; got %v, wanted %v", file.Name(), n, file.Size())
			}
		}
		check(tw.Close())
		check(pw.CloseWithError(io.EOF))
	}()
	zw := gzip.NewWriter(fw)
	n, err := io.Copy(zw, pr)
	if err != nil {
		log.Fatalf("Error copying to gzip writer: after %d bytes, %v", n, err)
	}
	if err := zw.Close(); err != nil {
		log.Fatalf("gzip.Close: %v", err)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage:\n")
	fmt.Fprintf(os.Stderr, "%s [-rev camlistore_revision | -rev WORKINPROGRESS -camlisource dir]\n", os.Args[0])
	flag.PrintDefaults()
	os.Exit(1)
}

func checkFlags() {
	if flag.NArg() != 0 {
		usage()
	}
	if *doBinaries && (*doImage || *doUpload) {
		fmt.Fprintf(os.Stderr, "Usage error: --build_binaries and --build_image are mutually exclusive.\n")
		usage()
	}
	if *rev == "" {
		usage()
	}
	if *rev == "WORKINPROGRESS" {
		if *localSrc == "" {
			usage()
		}
		return
	}
	if *localSrc != "" {
		fmt.Fprintf(os.Stderr, "Usage error: --camlisource can only be used with --rev WORKINPROGRESS.\n")
		usage()
	}
}

func main() {
	flag.Usage = usage
	flag.Parse()
	checkFlags()

	camDir, err := osutil.GoPackagePath("camlistore.org")
	if err != nil {
		log.Fatalf("Error looking up camlistore.org dir: %v", err)
	}
	dockDir = filepath.Join(camDir, "misc", "docker")

	if *doImage {
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

	// TODO(mpl): maybe *doBinaries should be done by a separate go program,
	// because the end product is not a docker image. However, we're still
	// using docker all along, and it's convenient for now for code reuse. I
	// can refactor it all out of dock.go afterwards if we like the process.
	if *doBinaries {
		// TODO(mpl): consider using an "official" or trusted existing
		// Go docker image, since we don't do anything special anymore in
		// ours?
		buildDockerImage("go", goDockerImage+"-linux")
		ctxDir, err := ioutil.TempDir("", "camli-build")
		if err != nil {
			log.Fatal(err)
		}
		defer os.RemoveAll(ctxDir)
		genBinaries(ctxDir)
		packBinaries(ctxDir)
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
