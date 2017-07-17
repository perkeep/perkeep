// +build ignore

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

// This program builds Camlistore.
//
// $ go run make.go
//
// See the BUILDING file.
//
// The output binaries go into the ./bin/ directory (under the
// Camlistore root, where make.go is)
package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var haveSQLite = checkHaveSQLite()

var (
	embedResources = flag.Bool("embed_static", true, "Whether to embed resources needed by the UI such as images, css, and javascript.")
	sqlFlag        = flag.String("sqlite", "false", "Whether you want SQLite in your build: true, false, or auto.")
	all            = flag.Bool("all", false, "Force rebuild of everything (go install -a)")
	race           = flag.Bool("race", false, "Build race-detector version of binaries (they will run slowly)")
	verbose        = flag.Bool("v", strings.Contains(os.Getenv("CAMLI_DEBUG_X"), "makego"), "Verbose mode")
	targets        = flag.String("targets", "", "Optional comma-separated list of targets (i.e go packages) to build and install. '*' builds everything.  Empty builds defaults for this platform. Example: camlistore.org/server/camlistored,camlistore.org/cmd/camput")
	quiet          = flag.Bool("quiet", false, "Don't print anything unless there's a failure.")
	onlysync       = flag.Bool("onlysync", false, "Only populate the temporary source/build tree and output its full path. It is meant to prepare the environment for running the full test suite with 'devcam test'.")
	ifModsSince    = flag.Int64("if_mods_since", 0, "If non-zero return immediately without building if there aren't any filesystem modifications past this time (in unix seconds)")
	buildARCH      = flag.String("arch", runtime.GOARCH, "Architecture to build for.")
	buildOS        = flag.String("os", runtime.GOOS, "Operating system to build for.")
	buildARM       = flag.String("arm", "7", "ARM version to use if building for ARM. Note that this version applies even if the host arch is ARM too (and possibly of a different version).")
	stampVersion   = flag.Bool("stampversion", true, "Stamp version into buildinfo.GitInfo")
	website        = flag.Bool("website", false, "Just build the website.")
	camnetdns      = flag.Bool("camnetdns", false, "Just build camlistore.org/server/camnetdns.")
	static         = flag.Bool("static", false, "Build a static binary, so it can run in an empty container.")

	// Use GOPATH from the environment and work from there. Do not create a temporary source tree with a new GOPATH in it.
	// It is set through CAMLI_MAKE_USEGOPATH for integration tests that call 'go run make.go', and which are already in
	// a temp GOPATH.
	useGoPath bool
)

var (
	// camRoot is the original Camlistore project root, from where the source files are mirrored.
	camRoot string
	// buildGoPath becomes our child "go" processes' GOPATH environment variable
	buildGoPath string
	// Our temporary source tree root and build dir, i.e: buildGoPath + "src/camlistore.org"
	buildSrcDir string
	// files mirrored from camRoot to buildSrcDir
	rxMirrored = regexp.MustCompile(`^([a-zA-Z0-9\-\_\.]+\.(?:blobs|camli|css|eot|err|gif|go|s|pb\.go|gpg|html|ico|jpg|js|json|xml|min\.css|min\.js|mp3|otf|png|svg|pdf|psd|tiff|ttf|woff|xcf|tar\.gz|gz|tar\.xz|tbz2|zip|sh))$`)
	// base file exceptions for the above matching, so as not to complicate the regexp any further
	mirrorIgnored = map[string]bool{
		"publisher.js": true, // because this file is (re)generated after the mirroring
		"goui.js":      true, // because this file is (re)generated after the mirroring
	}
	// gopherjsGoroot should be specified through the env var
	// CAMLI_GOPHERJS_GOROOT when the user's using go tip, because gopherjs only
	// builds with Go 1.8.
	gopherjsGoroot string
)

func main() {
	log.SetFlags(0)
	flag.Parse()

	if *buildARCH == "386" && *buildOS == "darwin" {
		if ok, _ := strconv.ParseBool(os.Getenv("CAMLI_FORCE_OSARCH")); !ok {
			log.Fatalf("You're trying to build a 32-bit binary for a Mac. That is almost always a mistake.\nTo do it anyway, set env CAMLI_FORCE_OSARCH=1 and run again.\n")
		}
	}

	if *website && *camnetdns {
		log.Fatal("-camnetdns and -website are mutually exclusive")
	}

	gopherjsGoroot = os.Getenv("CAMLI_GOPHERJS_GOROOT")

	verifyGoVersion()

	sql := withSQLite()
	if useEnvGoPath, _ := strconv.ParseBool(os.Getenv("CAMLI_MAKE_USEGOPATH")); useEnvGoPath {
		useGoPath = true
	}
	latestSrcMod := time.Now()
	if useGoPath {
		buildGoPath = os.Getenv("GOPATH")
		var err error
		camRoot, err = goPackagePath("camlistore.org")
		if err != nil {
			log.Fatalf("Cannot run make.go with --use_gopath: %v (is GOPATH not set?)", err)
		}
		buildSrcDir = camRoot
		if *ifModsSince > 0 {
			latestSrcMod = walkDirs(sql)
		}
	} else {
		var err error
		camRoot, err = os.Getwd()
		if err != nil {
			log.Fatalf("Failed to get current directory: %v", err)
		}
		latestSrcMod = mirror(sql)
		if *onlysync {
			if *website {
				log.Fatal("-onlysync and -website are mutually exclusive")
			}
			if *camnetdns {
				log.Fatal("-onlysync and -camnetdns are mutually exclusive")
			}
			mirrorFile("make.go", filepath.Join(buildSrcDir, "make.go"))
			// Since we have not done the resources embedding, the
			// z_*.go files have not been marked as wanted and are
			// going to be removed. And they will have to be
			// regenerated next time make.go is run.
			deleteUnwantedOldMirrorFiles(buildSrcDir, true)
			fmt.Println(buildGoPath)
			return
		}
	}
	if latestSrcMod.Before(time.Unix(*ifModsSince, 0)) {
		return
	}
	binDir := filepath.Join(camRoot, "bin")
	version := getVersion()

	if *verbose {
		log.Printf("Camlistore version = %s", version)
		log.Printf("SQLite included: %v", sql)
		log.Printf("Temporary source: %s", buildSrcDir)
		log.Printf("Output binaries: %s", binDir)
	}

	buildAll := false
	targs := []string{
		"camlistore.org/dev/devcam",
		"camlistore.org/cmd/camget",
		"camlistore.org/cmd/camput",
		"camlistore.org/cmd/camtool",
		"camlistore.org/cmd/camdeploy",
		"camlistore.org/server/camlistored",
		"camlistore.org/app/hello",
		"camlistore.org/app/publisher",
		"camlistore.org/app/scanningcabinet",
		"camlistore.org/app/scanningcabinet/scancab",
	}
	switch *targets {
	case "*":
		buildAll = true
	case "":
		// Add cammount to default build targets on OSes that support FUSE.
		switch *buildOS {
		case "linux", "darwin":
			targs = append(targs, "camlistore.org/cmd/cammount")
		}
	default:
		if *website {
			log.Fatal("-targets and -website are mutually exclusive")
		}
		if *camnetdns {
			log.Fatal("-targets and -camnetdns are mutually exclusive")
		}
		if t := strings.Split(*targets, ","); len(t) != 0 {
			targs = t
		}
	}
	if *website || *camnetdns {
		buildAll = false
		if *website {
			targs = []string{"camlistore.org/website"}
		} else if *camnetdns {
			targs = []string{"camlistore.org/server/camnetdns"}
		}
	}

	withCamlistored := stringListContains(targs, "camlistore.org/server/camlistored")

	// TODO(mpl): no need to build publisher.js if we're not building the publisher app.
	if withCamlistored {
		// gopherjs has to run before doEmbed since we need all the javascript
		// to be generated before embedding happens.
		if err := makeGopherjs(); err != nil {
			log.Fatal(err)
		}
	}

	if *embedResources && withCamlistored {
		doEmbed()
	}

	if !useGoPath {
		deleteUnwantedOldMirrorFiles(buildSrcDir, withCamlistored)
	}

	tags := []string{"purego"} // for cznic/zappy
	if *static {
		tags = append(tags, "netgo")
	}
	if sql {
		tags = append(tags, "with_sqlite")
	}
	baseArgs := []string{"install", "-v"}
	if *all {
		baseArgs = append(baseArgs, "-a")
	}
	if *race {
		baseArgs = append(baseArgs, "-race")
	}
	if *verbose {
		log.Printf("version to stamp is %q", version)
	}
	var ldFlags string
	if *static {
		ldFlags = "-w -d -linkmode internal"
	}
	if *stampVersion {
		if ldFlags != "" {
			ldFlags += " "
		}
		ldFlags += "-X \"camlistore.org/pkg/buildinfo.GitInfo=" + version + "\""
	}
	baseArgs = append(baseArgs, "--ldflags="+ldFlags, "--tags="+strings.Join(tags, " "))

	// First install command: build just the final binaries, installed to a GOBIN
	// under <camlistore_root>/bin:
	args := append(baseArgs, targs...)

	if buildAll {
		args = append(args,
			"camlistore.org/app/...",
			"camlistore.org/pkg/...",
			"camlistore.org/server/...",
			"camlistore.org/internal/...",
		)
	}

	cmd := exec.Command("go", args...)
	cmd.Env = append(cleanGoEnv(),
		"GOPATH="+buildGoPath,
	)
	if *static {
		cmd.Env = append(cmd.Env, "CGO_ENABLED=0")
	}

	if *verbose {
		log.Printf("Running go %q with Env %q", args, cmd.Env)
	}

	var output bytes.Buffer
	if *quiet {
		cmd.Stdout = &output
		cmd.Stderr = &output
	} else {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	if *verbose {
		log.Printf("Running go install of main binaries with args %s", cmd.Args)
	}
	if err := cmd.Run(); err != nil {
		log.Fatalf("Error building main binaries: %v\n%s", err, output.String())
	}

	// Copy the binaries from $CAMROOT/tmp/build-gopath-foo/bin to $CAMROOT/bin.
	// This is necessary (instead of just using GOBIN environment variable) so
	// each tmp/build-gopath-* has its own binary modtimes for its own build tags.
	// Otherwise switching sqlite true<->false doesn't necessarily cause a rebuild.
	// See camlistore.org/issue/229
	for _, targ := range targs {
		src := exeName(filepath.Join(actualBinDir(filepath.Join(buildGoPath, "bin")), pathpkg.Base(targ)))
		dst := exeName(filepath.Join(actualBinDir(binDir), pathpkg.Base(targ)))
		if err := mirrorFile(src, dst); err != nil {
			log.Fatalf("Error copying %s to %s: %v", src, dst, err)
		}
	}

	if !*quiet {
		log.Printf("Success. Binaries are in %s", actualBinDir(binDir))
	}
}

func baseDirName(sql bool) string {
	buildBaseDir := "build-gopath"
	if !sql {
		buildBaseDir += "-nosqlite"
	}
	// We don't even consider whether we're cross-compiling. As long as we
	// build for ARM, we do it in its own versioned dir.
	if *buildARCH == "arm" {
		buildBaseDir += "-armv" + *buildARM
	}
	return buildBaseDir
}

const (
	publisherJS = "app/publisher/publisher.js"
	gopherjsUI  = "server/camlistored/ui/goui.js"
)

// buildGopherjs builds the gopherjs binary from our vendored gopherjs source.
// It returns the path to the binary if successful, an error otherwise.
func buildGopherjs() (string, error) {
	src := filepath.Join(buildSrcDir, filepath.FromSlash("vendor/github.com/gopherjs/gopherjs"))
	// Note: do not use exeName for gopherjs, as it will run on the current platform,
	// not on the one we're cross-compiling for.
	bin := filepath.Join(buildGoPath, "bin", "gopherjs")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	var srcModtime, binModtime time.Time
	if err := filepath.Walk(src, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		if t := fi.ModTime(); t.After(srcModtime) {
			srcModtime = t
		}
		return nil
	}); err != nil {
		return "", err
	}
	fi, err := os.Stat(bin)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", err
		}
		binModtime = srcModtime
	} else {
		binModtime = fi.ModTime()
	}
	if binModtime.After(srcModtime) {
		return bin, nil
	}
	log.Printf("Now rebuilding gopherjs at %v", bin)
	goBin := "go"
	if gopherjsGoroot != "" {
		goBin = filepath.Join(gopherjsGoroot, "bin", "go")
	}
	cmd := exec.Command(goBin, "install")
	cmd.Dir = src
	cmd.Env = append(cleanGoEnv(),
		"GOPATH="+buildGoPath,
	)
	// forcing GOOS and GOARCH to prevent cross-compiling, as gopherjs will run on the
	// current (host) platform.
	cmd.Env = setEnv(cmd.Env, "GOOS", runtime.GOOS)
	cmd.Env = setEnv(cmd.Env, "GOARCH", runtime.GOARCH)
	if gopherjsGoroot != "" {
		cmd.Env = setEnv(cmd.Env, "GOROOT", gopherjsGoroot)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("error while building gopherjs: %v, %v", err, string(out))
	}
	return bin, nil
}

// For some reason (https://github.com/gopherjs/gopherjs/issues/415), the
// github.com/gopherjs/gopherjs/js import is treated specially, and it cannot be
// vendored at all for gopherjs to work properly. So we move it to our tmp GOPATH.
func moveGopherjs() error {
	dest := filepath.Join(buildGoPath, filepath.FromSlash("src/github.com/gopherjs/gopherjs"))
	if err := os.MkdirAll(dest, 0700); err != nil {
		return err
	}
	src := filepath.Join(buildSrcDir, filepath.FromSlash("vendor/github.com/gopherjs/gopherjs"))
	if err := filepath.Walk(src, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		suffix, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		destName := filepath.Join(dest, suffix)
		if fi.IsDir() {
			return os.MkdirAll(destName, 0700)
		}
		destFi, err := os.Stat(destName)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && !fi.ModTime().After(destFi.ModTime()) {
			return nil
		}
		dataSrc, err := ioutil.ReadFile(path)
		if err != nil {
			return err
		}
		return ioutil.WriteFile(destName, dataSrc, 0600)
	}); err != nil {
		return err
	}
	return os.RemoveAll(src)
}

// genSearchTypes duplicates some of the camlistore.org/pkg/search types into
// camlistore.org/app/publisher/js/zsearch.go , because it's too costly (in output
// file size) for now to import the search pkg into gopherjs.
func genSearchTypes() error {
	sourceFile := filepath.Join(buildSrcDir, filepath.FromSlash("pkg/search/describe.go"))
	outputFile := filepath.Join(buildSrcDir, filepath.FromSlash("app/publisher/js/zsearch.go"))
	fi1, err := os.Stat(sourceFile)
	if err != nil {
		return err
	}
	fi2, err := os.Stat(outputFile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil && fi2.ModTime().After(fi1.ModTime()) {
		wantDestFile[outputFile] = true
		return nil
	}
	args := []string{"generate", "camlistore.org/app/publisher/js"}
	cmd := exec.Command("go", args...)
	cmd.Env = append(cleanGoEnv(),
		"GOPATH="+buildGoPath,
	)
	cmd.Env = setEnv(cmd.Env, "GOOS", runtime.GOOS)
	cmd.Env = setEnv(cmd.Env, "GOARCH", runtime.GOARCH)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go generate for publisher js error: %v, %v", err, string(out))
	}
	wantDestFile[outputFile] = true
	log.Printf("generated %v", outputFile)
	return nil
}

// genPublisherJS runs the gopherjs command, using the gopherjsBin binary, on
// camlistore.org/app/publisher/js, to generate the javascript code at
// app/publisher/publisher.js
func genPublisherJS(gopherjsBin string) error {
	if err := genSearchTypes(); err != nil {
		return err
	}
	// Run gopherjs on a temporary output file, so we don't change the
	// modtime of the existing gopherjs.js if there was no reason to.
	output := filepath.Join(buildSrcDir, filepath.FromSlash(publisherJS))
	tmpOutput := output + ".new"
	args := []string{"build", "--tags", "nocgo"}
	if *embedResources {
		// when embedding for "production", use -m to minify the javascript output
		args = append(args, "-m")
	}
	args = append(args, "-o", tmpOutput, "camlistore.org/app/publisher/js")
	cmd := exec.Command(gopherjsBin, args...)
	cmd.Env = append(cleanGoEnv(),
		"GOPATH="+buildGoPath,
	)
	// Pretend we're on linux regardless of the actual host, because recommended
	// hack to work around https://github.com/gopherjs/gopherjs/issues/511
	cmd.Env = setEnv(cmd.Env, "GOOS", "linux")
	if gopherjsGoroot != "" {
		cmd.Env = setEnv(cmd.Env, "GOROOT", gopherjsGoroot)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gopherjs for publisher error: %v, %v", err, string(out))
	}

	// check if new output is different from previous run result
	_, err := os.Stat(output)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	needsUpdate := true
	if err == nil {
		if hashsum(tmpOutput) == hashsum(output) {
			needsUpdate = false
		}
	}
	if needsUpdate {
		// general case: replace previous run result with new output
		if err := os.Rename(tmpOutput, output); err != nil {
			return err
		}
		log.Printf("gopherjs generated %v", output)
	}
	// And since we're generating after the mirroring, we need to manually
	// add the output to the wanted files
	wantDestFile[output] = true
	wantDestFile[output+".map"] = true

	// Finally, even when embedding resources, we copy the output back to
	// camRoot. It's a bit unsatisfactory that we have to modify things out of
	// buildGoPath but it's better than the alternative (the user ending up
	// without a copy of publisher.js in their camRoot).
	jsInCamRoot := filepath.Join(camRoot, filepath.FromSlash(publisherJS))
	if !needsUpdate {
		_, err := os.Stat(jsInCamRoot)
		if err == nil {
			return nil
		}
		if !os.IsNotExist(err) {
			log.Fatal(err)
		}
	}
	data, err := ioutil.ReadFile(output)
	if err != nil {
		return err
	}
	if err := ioutil.WriteFile(
		jsInCamRoot,
		data, 0600); err != nil {
		return err
	}
	log.Printf("Copied gopherjs generated code to  %v", jsInCamRoot)
	return nil
}

// TODO(mpl): refactor genWebUIJS with genPublisherJS

// genWebUIJS runs the gopherjs command, using the gopherjsBin binary, on
// camlistore.org/server/camlistored/ui/goui, to generate the javascript
// code at camlistore.org/server/camlistored/ui/goui.js
func genWebUIJS(gopherjsBin string) error {
	// Run gopherjs on a temporary output file, so we don't change the
	// modtime of the existing goui.js if there was no reason to.
	output := filepath.Join(buildSrcDir, filepath.FromSlash(gopherjsUI))
	tmpOutput := output + ".new"
	args := []string{"build", "--tags", "nocgo"}
	if *embedResources {
		// when embedding for "production", use -m to minify the javascript output
		args = append(args, "-m")
	}
	args = append(args, "-o", tmpOutput, "camlistore.org/server/camlistored/ui/goui")
	cmd := exec.Command(gopherjsBin, args...)
	cmd.Env = append(cleanGoEnv(),
		"GOPATH="+buildGoPath,
	)
	// Pretend we're on linux regardless of the actual host, because recommended
	// hack to work around https://github.com/gopherjs/gopherjs/issues/511
	cmd.Env = setEnv(cmd.Env, "GOOS", "linux")
	if gopherjsGoroot != "" {
		cmd.Env = setEnv(cmd.Env, "GOROOT", gopherjsGoroot)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gopherjs for web UI error: %v, %v", err, string(out))
	}

	// check if new output is different from previous run result
	_, err := os.Stat(output)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	needsUpdate := true
	if err == nil {
		if hashsum(tmpOutput) == hashsum(output) {
			needsUpdate = false
		}
	}
	if needsUpdate {
		// general case: replace previous run result with new output
		if err := os.Rename(tmpOutput, output); err != nil {
			return err
		}
		log.Printf("gopherjs for web UI generated %v", output)
	}
	// And since we're generating after the mirroring, we need to manually
	// add the output to the wanted files
	wantDestFile[output] = true
	wantDestFile[output+".map"] = true

	// Finally, even when embedding resources, we copy the output back to
	// camRoot. It's a bit unsatisfactory that we have to modify things out of
	// buildGoPath but it's better than the alternative (the user ending up
	// without a copy of publisher.js in their camRoot).
	jsInCamRoot := filepath.Join(camRoot, filepath.FromSlash(gopherjsUI))
	if !needsUpdate {
		_, err := os.Stat(jsInCamRoot)
		if err == nil {
			return nil
		}
		if !os.IsNotExist(err) {
			log.Fatal(err)
		}
	}
	data, err := ioutil.ReadFile(output)
	if err != nil {
		return err
	}
	if err := ioutil.WriteFile(
		jsInCamRoot,
		data, 0600); err != nil {
		return err
	}
	log.Printf("Copied gopherjs generated code for web UI to  %v", jsInCamRoot)
	return nil
}

// noGopherJS creates a fake (unusable) gopherjs.js file for when we want to skip all of
// the gopherjs business.
func noGopherJS(output string) {
	if err := ioutil.WriteFile(
		output,
		[]byte("// This (broken) output should only be generated when CAMLI_MAKE_USEGOPATH is set, which should be only for integration tests.\n"),
		0600); err != nil {
		log.Fatal(err)
	}
}

func hashsum(filename string) string {
	h := sha256.New()
	f, err := os.Open(filename)
	if err != nil {
		log.Fatalf("could not compute SHA256 of %v: %v", filename, err)
	}
	defer f.Close()
	if _, err := io.Copy(h, f); err != nil {
		log.Fatalf("could not compute SHA256 of %v: %v", filename, err)
	}
	return string(h.Sum(nil))
}

// makeGopherjs builds and runs the gopherjs command on camlistore.org/app/publisher/js
// and camlistore.org/server/camlistored/ui/goui
// When CAMLI_MAKE_USEGOPATH is set (for integration tests through devcam), we
// generate a fake file instead.
func makeGopherjs() error {
	if useGoPath {
		noGopherJS(filepath.Join(buildSrcDir, filepath.FromSlash(publisherJS)))
		return nil
	}
	gopherjs, err := buildGopherjs()
	if err != nil {
		return fmt.Errorf("error building gopherjs: %v", err)
	}

	// TODO(mpl): remove when https://github.com/gopherjs/gopherjs/issues/415 is fixed.
	if err := moveGopherjs(); err != nil {
		return err
	}

	if err := genPublisherJS(gopherjs); err != nil {
		return err
	}
	if err := genWebUIJS(gopherjs); err != nil {
		return err
	}
	return nil
}

// create the tmp GOPATH, and mirror to it from camRoot.
// return the latest modtime among all of the walked files.
func mirror(sql bool) (latestSrcMod time.Time) {
	verifyCamlistoreRoot(camRoot)

	buildBaseDir := baseDirName(sql)

	buildGoPath = filepath.Join(camRoot, "tmp", buildBaseDir)
	buildSrcDir = filepath.Join(buildGoPath, "src", "camlistore.org")

	if err := os.MkdirAll(buildSrcDir, 0755); err != nil {
		log.Fatal(err)
	}

	// We copy all *.go files from camRoot's goDirs to buildSrcDir.
	goDirs := []string{
		"app",
		"cmd",
		"dev",
		"internal",
		"pkg",
		"server/camlistored",
		"vendor",
	}
	if *onlysync {
		goDirs = append(goDirs, "server/appengine", "config", "misc", "./website")
	}
	if *website {
		goDirs = []string{
			"pkg",
			"vendor",
			"website",
		}
	} else if *camnetdns {
		goDirs = []string{
			"pkg",
			"vendor",
			"server/camnetdns",
		}
	}
	// Copy files we do want in our mirrored GOPATH.  This has the side effect of
	// populating wantDestFile, populated by mirrorFile.
	for _, dir := range goDirs {
		srcPath := filepath.Join(camRoot, filepath.FromSlash(dir))
		dstPath := buildSrcPath(dir)
		if maxMod, err := mirrorDir(srcPath, dstPath, walkOpts{sqlite: sql}); err != nil {
			log.Fatalf("Error while mirroring %s to %s: %v", srcPath, dstPath, err)
		} else {
			if maxMod.After(latestSrcMod) {
				latestSrcMod = maxMod
			}
		}
	}
	return
}

// TODO(mpl): see if walkDirs and mirror can be refactored further.

// walk all the dirs in camRoot, to return the latest
// modtime among all of the walked files.
func walkDirs(sql bool) (latestSrcMod time.Time) {
	d, err := os.Open(camRoot)
	if err != nil {
		log.Fatal(err)
	}
	dirs, err := d.Readdirnames(-1)
	d.Close()
	if err != nil {
		log.Fatal(err)
	}

	for _, dir := range dirs {
		srcPath := filepath.Join(camRoot, filepath.FromSlash(dir))
		if maxMod, err := walkDir(srcPath, walkOpts{sqlite: sql}); err != nil {
			log.Fatalf("Error while walking %s: %v", srcPath, err)
		} else {
			if maxMod.After(latestSrcMod) {
				latestSrcMod = maxMod
			}
		}
	}
	return
}

func actualBinDir(dir string) string {
	if *buildARCH == runtime.GOARCH && *buildOS == runtime.GOOS {
		return dir
	}
	return filepath.Join(dir, *buildOS+"_"+*buildARCH)
}

// Create an environment variable of the form key=value.
func envPair(key, value string) string {
	return fmt.Sprintf("%s=%s", key, value)
}

// cleanGoEnv returns a copy of the current environment with GOPATH and GOBIN removed.
// it also sets GOOS and GOARCH as needed when cross-compiling.
func cleanGoEnv() (clean []string) {
	for _, env := range os.Environ() {
		if strings.HasPrefix(env, "GOPATH=") || strings.HasPrefix(env, "GOBIN=") {
			continue
		}
		// We skip these two as well, otherwise they'd take precedence over the
		// ones appended below.
		if *buildOS != runtime.GOOS && strings.HasPrefix(env, "GOOS=") {
			continue
		}
		if *buildARCH != runtime.GOARCH && strings.HasPrefix(env, "GOARCH=") {
			continue
		}
		// If we're building for ARM (regardless of cross-compiling or not), we reset GOARM
		if *buildARCH == "arm" && strings.HasPrefix(env, "GOARM=") {
			continue
		}

		clean = append(clean, env)
	}
	if *buildOS != runtime.GOOS {
		clean = append(clean, envPair("GOOS", *buildOS))
	}
	if *buildARCH != runtime.GOARCH {
		clean = append(clean, envPair("GOARCH", *buildARCH))
	}
	// If we're building for ARM (regardless of cross-compiling or not), we reset GOARM
	if *buildARCH == "arm" {
		clean = append(clean, envPair("GOARM", *buildARM))
	}
	return
}

// setEnv sets the given key & value in the provided environment.
// Each value in the env list should be of the form key=value.
func setEnv(env []string, key, value string) []string {
	for i, s := range env {
		if strings.HasPrefix(s, fmt.Sprintf("%s=", key)) {
			env[i] = envPair(key, value)
			return env
		}
	}
	env = append(env, envPair(key, value))
	return env
}

func stringListContains(strs []string, str string) bool {
	for _, s := range strs {
		if s == str {
			return true
		}
	}
	return false
}

// buildSrcPath returns the full path concatenation
// of buildSrcDir with fromSrc.
func buildSrcPath(fromSrc string) string {
	return filepath.Join(buildSrcDir, filepath.FromSlash(fromSrc))
}

// genEmbeds generates from the static resources the zembed.*.go
// files that will allow for these resources to be included in
// the camlistored binary.
// It also populates wantDestFile with those files so they're
// kept in between runs.
func genEmbeds() error {
	// Note: do not use exeName for genfileembed, as it will run on the current platform,
	// not on the one we're cross-compiling for.
	cmdName := filepath.Join(buildGoPath, "bin", "genfileembed")
	if runtime.GOOS == "windows" {
		cmdName += ".exe"
	}
	for _, embeds := range []string{"server/camlistored/ui", "pkg/server", "vendor/embed/react", "vendor/embed/less", "vendor/embed/glitch", "vendor/embed/fontawesome", "vendor/embed/leaflet", "app/publisher", "app/scanningcabinet/ui"} {
		embeds := buildSrcPath(embeds)
		args := []string{"--output-files-stderr", embeds}
		cmd := exec.Command(cmdName, args...)
		cmd.Env = append(cleanGoEnv(),
			"GOPATH="+buildGoPath,
		)
		cmd.Stdout = os.Stdout
		stderr, err := cmd.StderrPipe()
		if err != nil {
			log.Fatal(err)
		}
		if *verbose {
			log.Printf("Running %s %s", cmdName, embeds)
		}
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("Error starting %s %s: %v", cmdName, embeds, err)
		}
		parseGenEmbedOutputLines(stderr)
		if err := cmd.Wait(); err != nil {
			return fmt.Errorf("Error running %s %s: %v", cmdName, embeds, err)
		}
	}
	return nil
}

func parseGenEmbedOutputLines(r io.Reader) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		ln := sc.Text()
		if !strings.HasPrefix(ln, "OUTPUT:") {
			continue
		}
		wantDestFile[strings.TrimSpace(strings.TrimPrefix(ln, "OUTPUT:"))] = true
	}
}

func buildGenfileembed() error {
	args := []string{"install", "-v"}
	if *all {
		args = append(args, "-a")
	}
	args = append(args,
		filepath.FromSlash("camlistore.org/pkg/fileembed/genfileembed"),
	)
	cmd := exec.Command("go", args...)

	// We don't even need to set GOBIN as it defaults to $GOPATH/bin
	// and that is where we want genfileembed to go.
	// Here we replace the GOOS and GOARCH valuesfrom the env with the host OS,
	// to support cross-compiling.
	cmd.Env = cleanGoEnv()
	cmd.Env = setEnv(cmd.Env, "GOPATH", buildGoPath)
	cmd.Env = setEnv(cmd.Env, "GOOS", runtime.GOOS)
	cmd.Env = setEnv(cmd.Env, "GOARCH", runtime.GOARCH)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if *verbose {
		log.Printf("Running go with args %s", args)
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Error building genfileembed: %v", err)
	}
	if *verbose {
		log.Printf("genfileembed installed in %s", filepath.Join(buildGoPath, "bin"))
	}
	return nil
}

// getVersion returns the version of Camlistore. Either from a VERSION file at the root,
// or from git.
func getVersion() string {
	slurp, err := ioutil.ReadFile(filepath.Join(camRoot, "VERSION"))
	if err == nil {
		return strings.TrimSpace(string(slurp))
	}
	return gitVersion()
}

var gitVersionRx = regexp.MustCompile(`\b\d\d\d\d-\d\d-\d\d-[0-9a-f]{10,10}\b`)

// gitVersion returns the git version of the git repo at camRoot as a
// string of the form "yyyy-mm-dd-xxxxxxx", with an optional trailing
// '+' if there are any local uncomitted modifications to the tree.
func gitVersion() string {
	cmd := exec.Command("git", "rev-list", "--max-count=1", "--pretty=format:'%ad-%h'",
		"--date=short", "--abbrev=10", "HEAD")
	cmd.Dir = camRoot
	out, err := cmd.Output()
	if err != nil {
		log.Fatalf("Error running git rev-list in %s: %v", camRoot, err)
	}
	v := strings.TrimSpace(string(out))
	if m := gitVersionRx.FindStringSubmatch(v); m != nil {
		v = m[0]
	} else {
		panic("Failed to find git version in " + v)
	}
	cmd = exec.Command("git", "diff", "--exit-code")
	cmd.Dir = camRoot
	if err := cmd.Run(); err != nil {
		v += "+"
	}
	return v
}

// verifyCamlistoreRoot crashes if dir isn't the Camlistore root directory.
func verifyCamlistoreRoot(dir string) {
	testFile := filepath.Join(dir, "pkg", "blob", "ref.go")
	if _, err := os.Stat(testFile); err != nil {
		log.Fatalf("make.go must be run from the Camlistore src root directory (where make.go is). Current working directory is %s", dir)
	}
}

const (
	goVersionMinor  = '8'
	gopherJSGoMinor = '8'
)

func verifyGoVersion() {
	_, err := exec.LookPath("go")
	if err != nil {
		log.Fatalf("Go doesn't appear to be installed ('go' isn't in your PATH). Install Go 1.%c or newer.", goVersionMinor)
	}
	out, err := exec.Command("go", "version").Output()
	if err != nil {
		log.Fatalf("Error checking Go version with the 'go' command: %v", err)
	}
	fields := strings.Fields(string(out))
	if len(fields) < 3 || !strings.HasPrefix(string(out), "go version ") {
		log.Fatalf("Unexpected output while checking 'go version': %q", out)
	}
	version := fields[2]
	if version == "devel" {
		verifyGopherjsGoroot()
		return
	}
	// this check is still needed for the "go1" case.
	if len(version) < len("go1.") {
		log.Fatalf("Your version of Go (%s) is too old. Camlistore requires Go 1.%c or later.", version, goVersionMinor)
	}
	minorChar := strings.TrimPrefix(version, "go1.")[0]
	if minorChar >= goVersionMinor && minorChar <= '9' {
		if minorChar != gopherJSGoMinor {
			verifyGopherjsGoroot()
		}
		return
	}
	log.Fatalf("Your version of Go (%s) is too old. Camlistore requires Go 1.%c or later.", version, goVersionMinor)
}

func verifyGopherjsGoroot() {
	goBin := filepath.Join(gopherjsGoroot, "bin", "go")
	if gopherjsGoroot == "" {
		gopherjsGoroot = filepath.Join(homeDir(), fmt.Sprintf("go1.%c", gopherJSGoMinor))
		goBin = filepath.Join(gopherjsGoroot, "bin", "go")
		log.Printf("You're using go != 1.%c, and CAMLI_GOPHERJS_GOROOT was not provided, so defaulting to %v for building gopherjs instead.", gopherJSGoMinor, goBin)
	}
	if _, err := os.Stat(goBin); err != nil {
		if !os.IsNotExist(err) {
			log.Fatal(err)
		}
		log.Fatalf("%v not found. You need to specify a go1.%c root in CAMLI_GOPHERJS_GOROOT for building gopherjs", goBin, gopherJSGoMinor)
	}
}

type walkOpts struct {
	dst    string // if non empty, mirror walked files to this destination.
	sqlite bool   // want sqlite package?
}

func walkDir(src string, opts walkOpts) (maxMod time.Time, err error) {
	err = filepath.Walk(src, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		base := fi.Name()
		if fi.IsDir() {
			if !opts.sqlite && strings.Contains(path, "mattn") && strings.Contains(path, "go-sqlite3") {
				return filepath.SkipDir
			}
			return nil
		}
		dir, _ := filepath.Split(path)
		parent := filepath.Base(dir)
		if (strings.HasPrefix(base, ".#") || !rxMirrored.MatchString(base)) && parent != "testdata" {
			return nil
		}
		if _, ok := mirrorIgnored[base]; ok {
			return nil
		}
		suffix, err := filepath.Rel(src, path)
		if err != nil {
			return fmt.Errorf("Failed to find Rel(%q, %q): %v", src, path, err)
		}
		if t := fi.ModTime(); t.After(maxMod) {
			maxMod = t
		}
		if opts.dst != "" {
			return mirrorFile(path, filepath.Join(opts.dst, suffix))
		}
		return nil
	})
	return
}

func mirrorDir(src, dst string, opts walkOpts) (maxMod time.Time, err error) {
	opts.dst = dst
	return walkDir(src, opts)
}

var wantDestFile = make(map[string]bool) // full dest filename => true

func isExecMode(mode os.FileMode) bool {
	return (mode & 0111) != 0
}

func mirrorFile(src, dst string) error {
	wantDestFile[dst] = true
	sfi, err := os.Stat(src)
	if err != nil {
		return err
	}
	if sfi.Mode()&os.ModeType != 0 {
		log.Fatalf("mirrorFile can't deal with non-regular file %s", src)
	}
	dfi, err := os.Stat(dst)
	if err == nil &&
		isExecMode(sfi.Mode()) == isExecMode(dfi.Mode()) &&
		(dfi.Mode()&os.ModeType == 0) &&
		dfi.Size() == sfi.Size() &&
		dfi.ModTime().Unix() == sfi.ModTime().Unix() {
		// Seems to not be modified.
		return nil
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
	if err == nil {
		err = os.Chmod(dst, sfi.Mode())
	}
	if err == nil {
		err = os.Chtimes(dst, sfi.ModTime(), sfi.ModTime())
	}
	return err
}

func deleteUnwantedOldMirrorFiles(dir string, withCamlistored bool) {
	filepath.Walk(dir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			log.Fatalf("Error stating while cleaning %s: %v", path, err)
		}
		if fi.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if !wantDestFile[path] {
			if !withCamlistored && (strings.HasPrefix(base, "zembed_") || strings.Contains(path, "z_data.go")) {
				// If we're not building the camlistored binary,
				// no need to clean up the embedded Closure, JS,
				// CSS, HTML, etc. Doing so would just mean we'd
				// have to put it back into place later.
				return nil
			}
			if *verbose {
				log.Printf("Deleting old file from temp build dir: %s", path)
			}
			return os.Remove(path)
		}
		return nil
	})
}

func withSQLite() bool {
	cross := runtime.GOOS != *buildOS || runtime.GOARCH != *buildARCH
	var sql bool
	var err error
	if *sqlFlag == "auto" {
		sql = !cross && haveSQLite
	} else {
		sql, err = strconv.ParseBool(*sqlFlag)
		if err != nil {
			log.Fatalf("Bad boolean --sql flag %q", *sqlFlag)
		}
	}

	if cross && sql {
		log.Fatalf("SQLite isn't available when cross-compiling to another OS. Set --sqlite=false.")
	}
	if sql && !haveSQLite {
		log.Printf("SQLite not found. Either install it, or run make.go with --sqlite=false  See https://code.google.com/p/camlistore/wiki/SQLite")
		switch runtime.GOOS {
		case "darwin":
			log.Printf("On OS X, run 'brew install sqlite3 pkg-config'. Get brew from http://mxcl.github.io/homebrew/")
		case "linux":
			log.Printf("On Linux, run 'sudo apt-get install libsqlite3-dev' or equivalent.")
		case "windows":
			log.Printf("SQLite is not easy on windows. Please see https://camlistore.org/doc/server-config#windows")
		}
		os.Exit(2)
	}
	return sql
}

func checkHaveSQLite() bool {
	if runtime.GOOS == "windows" {
		// TODO: Find some other non-pkg-config way to test, like
		// just compiling a small Go program that sees whether
		// it's available.
		//
		// For now:
		return false
	}
	_, err := exec.LookPath("pkg-config")
	if err != nil {
		return false
	}
	out, err := exec.Command("pkg-config", "--libs", "sqlite3").Output()
	if err != nil && err.Error() == "exit status 1" {
		// This is sloppy (comparing against a string), but
		// doing it correctly requires using multiple *.go
		// files to portably get the OS-syscall bits, and I
		// want to keep make.go a single file.
		return false
	}
	if err != nil {
		log.Fatalf("Can't determine whether sqlite3 is available, and where. pkg-config error was: %v, %s", err, out)
	}
	return strings.TrimSpace(string(out)) != ""
}

func doEmbed() {
	if *verbose {
		log.Printf("Embedding resources...")
	}
	closureEmbed := buildSrcPath("server/camlistored/ui/closure/z_data.go")
	closureSrcDir := filepath.Join(camRoot, filepath.FromSlash("vendor/embed/closure/lib"))
	err := embedClosure(closureSrcDir, closureEmbed)
	if err != nil {
		log.Fatal(err)
	}
	wantDestFile[closureEmbed] = true
	if err = buildGenfileembed(); err != nil {
		log.Fatal(err)
	}
	if err = genEmbeds(); err != nil {
		log.Fatal(err)
	}
}

func embedClosure(closureDir, embedFile string) error {
	if _, err := os.Stat(closureDir); err != nil {
		return fmt.Errorf("Could not stat %v: %v", closureDir, err)
	}

	// first collect the files and modTime
	var modTime time.Time
	type pathAndSuffix struct {
		path, suffix string
	}
	var files []pathAndSuffix
	err := filepath.Walk(closureDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		suffix, err := filepath.Rel(closureDir, path)
		if err != nil {
			return fmt.Errorf("Failed to find Rel(%q, %q): %v", closureDir, path, err)
		}
		if fi.IsDir() {
			return nil
		}
		if mt := fi.ModTime(); mt.After(modTime) {
			modTime = mt
		}
		files = append(files, pathAndSuffix{path, suffix})
		return nil
	})
	if err != nil {
		return err
	}
	// do not regenerate the whole embedFile if it exists and newer than modTime.
	if fi, err := os.Stat(embedFile); err == nil && fi.Size() > 0 && fi.ModTime().After(modTime) {
		if *verbose {
			log.Printf("skipping regeneration of %s", embedFile)
		}
		return nil
	}

	// second, zip it
	var zipbuf bytes.Buffer
	var zipdest io.Writer = &zipbuf
	if os.Getenv("CAMLI_WRITE_TMP_ZIP") != "" {
		f, _ := os.Create("/tmp/camli-closure.zip")
		zipdest = io.MultiWriter(zipdest, f)
		defer f.Close()
	}
	w := zip.NewWriter(zipdest)
	for _, elt := range files {
		b, err := ioutil.ReadFile(elt.path)
		if err != nil {
			return err
		}
		f, err := w.Create(filepath.ToSlash(elt.suffix))
		if err != nil {
			return err
		}
		if _, err = f.Write(b); err != nil {
			return err
		}
	}
	if err = w.Close(); err != nil {
		return err
	}

	// then embed it as a quoted string
	var qb bytes.Buffer
	fmt.Fprint(&qb, "package closure\n\n")
	fmt.Fprint(&qb, "import \"time\"\n\n")
	fmt.Fprint(&qb, "func init() {\n")
	fmt.Fprintf(&qb, "\tZipModTime = time.Unix(%d, 0)\n", modTime.Unix())
	fmt.Fprint(&qb, "\tZipData = ")
	quote(&qb, zipbuf.Bytes())
	fmt.Fprint(&qb, "\n}\n")

	// and write to a .go file
	if err := writeFileIfDifferent(embedFile, qb.Bytes()); err != nil {
		return err
	}
	return nil

}

func writeFileIfDifferent(filename string, contents []byte) error {
	fi, err := os.Stat(filename)
	if err == nil && fi.Size() == int64(len(contents)) && contentsEqual(filename, contents) {
		return nil
	}
	return ioutil.WriteFile(filename, contents, 0644)
}

func contentsEqual(filename string, contents []byte) bool {
	got, err := ioutil.ReadFile(filename)
	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		log.Fatalf("Error reading %v: %v", filename, err)
	}
	return bytes.Equal(got, contents)
}

// quote escapes and quotes the bytes from bs and writes
// them to dest.
func quote(dest *bytes.Buffer, bs []byte) {
	dest.WriteByte('"')
	for _, b := range bs {
		if b == '\n' {
			dest.WriteString(`\n`)
			continue
		}
		if b == '\\' {
			dest.WriteString(`\\`)
			continue
		}
		if b == '"' {
			dest.WriteString(`\"`)
			continue
		}
		if (b >= 32 && b <= 126) || b == '\t' {
			dest.WriteByte(b)
			continue
		}
		fmt.Fprintf(dest, "\\x%02x", b)
	}
	dest.WriteByte('"')
}

func exeName(s string) string {
	if *buildOS == "windows" {
		return s + ".exe"
	}
	return s
}

// goPackagePath returns the path to the provided Go package's
// source directory.
// pkg may be a path prefix without any *.go files.
// The error is os.ErrNotExist if GOPATH is unset or the directory
// doesn't exist in any GOPATH component.
func goPackagePath(pkg string) (path string, err error) {
	gp := os.Getenv("GOPATH")
	if gp == "" {
		return path, os.ErrNotExist
	}
	for _, p := range filepath.SplitList(gp) {
		dir := filepath.Join(p, "src", filepath.FromSlash(pkg))
		fi, err := os.Stat(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if !fi.IsDir() {
			continue
		}
		return dir, nil
	}
	return path, os.ErrNotExist
}

// copied from pkg/osutil/paths.go
func homeDir() string {
	if runtime.GOOS == "windows" {
		return os.Getenv("HOMEDRIVE") + os.Getenv("HOMEPATH")
	}
	return os.Getenv("HOME")
}
