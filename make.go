//go:build ignore
// +build ignore

/*
Copyright 2013 The Perkeep Authors

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

// This program builds Perkeep.
//
// $ go run make.go
//
// See the BUILDING file.
//
// The output binaries go into the usual go install directory:
// $GOBIN or $GOPATH/bin.
package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
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

var (
	embedResources   = flag.Bool("embed_static", true, "Whether to embed resources needed by the UI such as images, css, and javascript.")
	race             = flag.Bool("race", false, "Build race-detector version of binaries (they will run slowly)")
	verbose          = flag.Bool("v", strings.Contains(os.Getenv("CAMLI_DEBUG_X"), "makego"), "Verbose mode")
	targets          = flag.String("targets", "", "Optional comma-separated list of targets (i.e go packages) to build and install. '*' builds everything.  Empty builds defaults for this platform. Example: perkeep.org/server/perkeepd,perkeep.org/cmd/pk-put")
	quiet            = flag.Bool("quiet", false, "Don't print anything unless there's a failure.")
	buildARCH        = flag.String("arch", runtime.GOARCH, "Architecture to build for.")
	buildOS          = flag.String("os", runtime.GOOS, "Operating system to build for.")
	buildARM         = flag.String("arm", "7", "ARM version to use if building for ARM. Note that this version applies even if the host arch is ARM too (and possibly of a different version).")
	stampVersion     = flag.Bool("stampversion", true, "Stamp version into buildinfo.GitInfo")
	website          = flag.Bool("website", false, "Just build the website.")
	camnetdns        = flag.Bool("camnetdns", false, "Just build perkeep.org/server/camnetdns.")
	static           = flag.Bool("static", false, "Build a static binary, so it can run in an empty container.")
	buildPublisherUI = flag.Bool("buildPublisherUI", false, "Rebuild the JS code of the web UI instead of fetching it from perkeep.org.")
	offline          = flag.Bool("offline", false, "Do not fetch the JS code for the web UI from perkeep.org. If not rebuilding the web UI, just trust the files on disk (if they exist).")
)

var (
	// pkRoot is the Perkeep project root
	pkRoot string
	binDir string // $GOBIN or $GOPATH/bin, based on user setting or default Go value.
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

	failIfCamlistoreOrgDir()
	verifyGoModules()
	verifyGoVersion()
	verifyPerkeepRoot()
	version := getVersion()
	gitRev := getGitVersion()

	if *verbose {
		log.Printf("Perkeep version = %q, git = %q", version, gitRev)
		log.Printf("Project source: %s", pkRoot)
		log.Printf("Output binaries: %s", actualBinDir())
	}

	buildAll := false
	targs := []string{
		"perkeep.org/dev/devcam",
		"perkeep.org/cmd/pk-get",
		"perkeep.org/cmd/pk-put",
		"perkeep.org/cmd/pk",
		"perkeep.org/cmd/pk-deploy",
		"perkeep.org/server/perkeepd",
		"perkeep.org/app/hello",
		"perkeep.org/app/publisher",
		"perkeep.org/app/scanningcabinet",
		"perkeep.org/app/scanningcabinet/scancab",
		"perkeep.org/app/webdav",
	}
	switch *targets {
	case "*":
		buildAll = true
	case "":
		// Add pk-mount to default build targets on OSes that support FUSE.
		switch *buildOS {
		case "linux", "darwin":
			targs = append(targs, "perkeep.org/cmd/pk-mount")
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
			targs = []string{"perkeep.org/website/pk-web"}
		} else if *camnetdns {
			targs = []string{"perkeep.org/server/camnetdns"}
		}
	}

	withPublisher := stringListContains(targs, "perkeep.org/app/publisher")
	if withPublisher {
		if err := doPublisherUI(); err != nil {
			log.Fatal(err)
		}
	}

	withPerkeepd := stringListContains(targs, "perkeep.org/server/perkeepd")
	if *embedResources && withPerkeepd {
		doEmbed()
	}

	tags := []string{"purego"} // for cznic/zappy
	if *static {
		tags = append(tags, "netgo")
	}
	if *embedResources {
		tags = append(tags, "with_embed")
	}
	baseArgs := []string{"install", "-v"}
	if *race {
		baseArgs = append(baseArgs, "-race")
	}
	if *verbose {
		log.Printf("version to stamp is %q, %q", version, gitRev)
	}
	var ldFlags string
	if *static {
		ldFlags = "-w -d -linkmode internal"
	}
	if *stampVersion {
		if ldFlags != "" {
			ldFlags += " "
		}
		ldFlags += "-X \"perkeep.org/pkg/buildinfo.GitInfo=" + gitRev + "\""
		ldFlags += "-X \"perkeep.org/pkg/buildinfo.Version=" + version + "\""
	}
	if ldFlags != "" {
		baseArgs = append(baseArgs, "--ldflags="+ldFlags)
	}
	baseArgs = append(baseArgs, "--tags="+strings.Join(tags, " "))

	// First install command: build just the final binaries, installed to a GOBIN
	// under <perkeep_root>/bin:
	args := append(baseArgs, targs...)

	if buildAll {
		args = append(args,
			"perkeep.org/app/...",
			"perkeep.org/pkg/...",
			"perkeep.org/server/...",
			"perkeep.org/internal/...",
		)
	}

	cmd := exec.Command("go", args...)
	cmd.Env = cleanGoEnv()
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

	if !*quiet {
		log.Printf("Success. Binaries are in %s", actualBinDir())
	}
}

func actualBinDir() string {
	cmd := exec.Command("go", "list", "-f", "{{.Target}}", "perkeep.org/cmd/pk")
	cmd.Env = cleanGoEnv()
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		log.Fatalf("Could not run go list to guess install dir: %v, %v", err, out)
	}
	return filepath.Dir(strings.TrimSpace(string(out)))
}

const (
	publisherJS    = "app/publisher/publisher.js"
	publisherJSURL = "https://storage.googleapis.com/perkeep-release/gopherjs/publisher.js"
)

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

// genSearchTypes duplicates some of the perkeep.org/pkg/search types into
// perkeep.org/app/publisher/js/zsearch.go , because it's too costly (in output
// file size) for now to import the search pkg into gopherjs.
func genSearchTypes() error {
	sourceFile := filepath.Join(pkRoot, filepath.FromSlash("pkg/search/describe.go"))
	outputFile := filepath.Join(pkRoot, filepath.FromSlash("app/publisher/js/zsearch.go"))
	fi1, err := os.Stat(sourceFile)
	if err != nil {
		return err
	}
	fi2, err := os.Stat(outputFile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil && fi2.ModTime().After(fi1.ModTime()) {
		return nil
	}
	cmd := exec.Command("go", "generate", "-tags=js", "-v", "perkeep.org/app/publisher/js")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go generate for publisher js error: %v, %v", err, string(out))
	}
	log.Printf("generated %v", outputFile)
	return nil
}

func genPublisherJS() error {
	if err := genSearchTypes(); err != nil {
		return err
	}
	output := filepath.Join(pkRoot, filepath.FromSlash(publisherJS))
	pkg := "perkeep.org/app/publisher/js"
	return genJS(pkg, output)
}

func goPathBinDir() (string, error) {
	cmd := exec.Command("go", "env", "GOPATH")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("could not get GOPATH: %v, %s", err, out)
	}
	paths := filepath.SplitList(strings.TrimSpace(string(out)))
	if len(paths) < 1 {
		return "", errors.New("no GOPATH")
	}
	return filepath.Join(paths[0], "bin"), nil
}

func genJS(pkg, output string) error {
	// We want to use 'gopherjs install', and not 'gopherjs build', as the former is
	// smarter and only rebuilds the output if needed. However, 'install' writes the
	// output to GOPATH/bin, and not GOBIN. (https://github.com/gopherjs/gopherjs/issues/494)
	// This means we have to be somewhat careful with naming our source pkg since gopherjs
	// derives its output name from it.
	// TODO(mpl): maybe rename the source pkg directories mentioned above.

	if err := runGopherJS(pkg); err != nil {
		return err
	}

	// TODO(mpl): set GOBIN, and remove all below, once
	// https://github.com/gopherjs/gopherjs/issues/494 is fixed
	binDir, err := goPathBinDir()
	if err != nil {
		return err
	}
	jsout := filepath.Join(binDir, filepath.Base(pkg)+".js")
	fi1, err1 := os.Stat(output)
	if err1 != nil && !os.IsNotExist(err1) {
		return err1
	}
	fi2, err2 := os.Stat(jsout)
	if err2 != nil && !os.IsNotExist(err2) {
		return err2
	}
	if err1 == nil && fi1.ModTime().After(fi2.ModTime()) {
		// output exists and is already up to date, nothing to do
		return nil
	}
	data, err := ioutil.ReadFile(jsout)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(output, data, 0600)
}

func runGopherJS(pkg string) error {
	args := []string{"run", "-mod=readonly", "github.com/goplusjs/gopherjs", "install", pkg, "-v", "--tags", "nocgo noReactBundle"}
	if *embedResources {
		// when embedding for "production", use -m to minify the javascript output
		args = append(args, "-m")
	}
	cmd := exec.Command("go", args...)
	cmd.Env = os.Environ()
	// Pretend we're on linux regardless of the actual host, because recommended
	// hack to work around https://github.com/gopherjs/gopherjs/issues/511
	cmd.Env = append(cmd.Env, "GOOS=linux")
	var buf bytes.Buffer
	cmd.Stderr = &buf
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("gopherjs for %v error: %v, %v", pkg, err, buf.String())
	}
	if *verbose {
		fmt.Println(buf.String())
	}
	return nil
}

// fetchJS gets the javascript resource at jsURL and writes it to jsOnDisk.
// Since said resource can be quite large, it first fetches the hashsum contained
// in the file at jsURL+".sha256", and if we already have the file on disk, with a
// matching hashsum, it does not actually fetch jsURL. If it does, it checks that
// the newly written file does match the hashsum.
func fetchJS(jsURL, jsOnDisk string) error {
	var currentSum string
	_, err := os.Stat(jsOnDisk)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
	} else {
		// If yes, compute its hash
		h := sha256.New()
		f, err := os.Open(jsOnDisk)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := io.Copy(h, f); err != nil {
			return err
		}
		currentSum = fmt.Sprintf("%x", h.Sum(nil))
	}

	// fetch the hash of the remote
	resp, err := http.Get(jsURL + ".sha256")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	upstreamSum := strings.TrimSuffix(string(data), "\n")

	if currentSum != "" &&
		currentSum == upstreamSum {
		// We already have the latest version
		return nil
	}

	resp, err = http.Get(jsURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	js := filepath.Join(pkRoot, filepath.FromSlash(jsOnDisk))
	f, err := os.Create(js)
	if err != nil {
		return err
	}
	h := sha256.New()
	mr := io.MultiWriter(f, h)
	if _, err := io.Copy(mr, resp.Body); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	sum := fmt.Sprintf("%x", h.Sum(nil))

	if upstreamSum != sum {
		return fmt.Errorf("checksum mismatch for %q: got %q, want %q", jsURL, sum, upstreamSum)
	}
	return nil
}

func doPublisherUI() error {
	if !*buildPublisherUI {
		if !*offline {
			return fetchJS(publisherJSURL, filepath.FromSlash(publisherJS))
		}
		_, err := os.Stat(filepath.FromSlash(publisherJS))
		if os.IsNotExist(err) {
			return fmt.Errorf("%s on disk is required for offline building. Fetch if first at %s.", publisherJS, publisherJSURL)
		}
		return err
	}

	if err := buildReactGen(); err != nil {
		return err
	}

	// gopherjs has to run before doEmbed since we need all the javascript
	// to be generated before embedding happens.
	return genPublisherJS()
}

// Create an environment variable of the form key=value.
func envPair(key, value string) string {
	return fmt.Sprintf("%s=%s", key, value)
}

// TODO(mpl): we probably can get rid of cleanGoEnv now that "last in wins" for
// duplicates in Env.

// cleanGoEnv returns a copy of the current environment with any variable listed
// in others removed. Also, when cross-compiling, it removes GOBIN and sets GOOS
// and GOARCH, and GOARM as needed.
func cleanGoEnv(others ...string) (clean []string) {
	excl := make([]string, len(others))
	for i, v := range others {
		excl[i] = v + "="
	}

Env:
	for _, env := range os.Environ() {
		for _, v := range excl {
			if strings.HasPrefix(env, v) {
				continue Env
			}
		}
		// remove GOBIN if we're cross-compiling
		if strings.HasPrefix(env, "GOBIN=") &&
			(*buildOS != runtime.GOOS || *buildARCH != runtime.GOARCH) {
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

func stringListContains(strs []string, str string) bool {
	for _, s := range strs {
		if s == str {
			return true
		}
	}
	return false
}

// fullSrcPath returns the full path concatenation
// of pkRoot with fromSrc.
func fullSrcPath(fromSrc string) string {
	return filepath.Join(pkRoot, filepath.FromSlash(fromSrc))
}

func buildReactGen() error {
	return buildBin("myitcv.io/react/cmd/reactGen")
}

func buildDevcam() error {
	return buildBin("perkeep.org/dev/devcam")
}

func buildBin(pkg string) error {
	pkgBase := pathpkg.Base(pkg)

	args := []string{"install", "-mod=readonly", "-v"}
	args = append(args,
		filepath.FromSlash(pkg),
	)
	cmd := exec.Command("go", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if *verbose {
		log.Printf("Running go with args %s", args)
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Error building %v: %v", pkgBase, err)
	}
	if *verbose {
		log.Printf("%v installed in %s", pkgBase, actualBinDir())
	}
	return nil
}

// getVersion returns the version of Perkeep found in a VERSION file at the root.
func getVersion() string {
	slurp, err := ioutil.ReadFile(filepath.Join(pkRoot, "VERSION"))
	v := strings.TrimSpace(string(slurp))
	if err != nil && !os.IsNotExist(err) {
		log.Fatal(err)
	}
	if v == "" {
		return "unknown"
	}
	return v
}

var gitVersionRx = regexp.MustCompile(`\b\d\d\d\d-\d\d-\d\d-[0-9a-f]{10,10}\b`)

// getGitVersion returns the git version of the git repo at pkRoot as a
// string of the form "yyyy-mm-dd-xxxxxxx", with an optional trailing
// '+' if there are any local uncommitted modifications to the tree.
func getGitVersion() string {
	if _, err := exec.LookPath("git"); err != nil {
		return ""
	}
	if _, err := os.Stat(filepath.Join(pkRoot, ".git")); os.IsNotExist(err) {
		return ""
	}
	cmd := exec.Command("git", "rev-list", "--max-count=1", "--pretty=format:'%ad-%h'",
		"--date=short", "--abbrev=10", "HEAD")
	cmd.Dir = pkRoot
	out, err := cmd.Output()
	if err != nil {
		log.Fatalf("Error running git rev-list in %s: %v", pkRoot, err)
	}
	v := strings.TrimSpace(string(out))
	if m := gitVersionRx.FindStringSubmatch(v); m != nil {
		v = m[0]
	} else {
		panic("Failed to find git version in " + v)
	}
	cmd = exec.Command("git", "diff", "--exit-code")
	cmd.Dir = pkRoot
	if err := cmd.Run(); err != nil {
		v += "+"
	}
	return v
}

// verifyPerkeepRoot sets pkRoot and crashes if dir isn't the Perkeep root directory.
func verifyPerkeepRoot() {
	var err error
	pkRoot, err = os.Getwd()
	if err != nil {
		log.Fatalf("Failed to get current directory: %v", err)
	}
	testFile := filepath.Join(pkRoot, "pkg", "blob", "ref.go")
	if _, err := os.Stat(testFile); err != nil {
		log.Fatalf("make.go must be run from the Perkeep src root directory (where make.go is). Current working directory is %s", pkRoot)
	}

	// we can't rely on perkeep.org/cmd/pk with modules on as we have no assurance
	// the current dir is $GOPATH/src/perkeep.org, so we use ./cmd/pk instead.
	cmd := exec.Command("go", "list", "-f", "{{.Target}}", "./cmd/pk")
	if os.Getenv("GO111MODULE") == "off" || *buildPublisherUI {
		// if we're building the webUI we need to be in "legacy" GOPATH mode, so in
		// $GOPATH/src/perkeep.org
		if err := validateDirInGOPATH(pkRoot); err != nil {
			log.Fatalf("We're running in GO111MODULE=off mode, either because you set it, or because you want to build the Web UI, so we need to be in a GOPATH, but: %v", err)
		}
		cmd = exec.Command("go", "list", "-f", "{{.Target}}", "perkeep.org/cmd/pk")
	}
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		log.Fatalf("Could not run go list to find install dir: %v, %s", err, out)
	}
	binDir = filepath.Dir(strings.TrimSpace(string(out)))
}

func validateDirInGOPATH(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}

	gopathEnv, err := exec.Command("go", "env", "GOPATH").Output()
	if err != nil {
		return fmt.Errorf("error finding GOPATH: %v", err)
	}
	gopaths := filepath.SplitList(strings.TrimSpace(string(gopathEnv)))
	if len(gopaths) == 0 {
		return fmt.Errorf("failed to find your GOPATH: go env GOPATH returned nothing")
	}
	var validOpts []string
	for _, gopath := range gopaths {
		validDir := filepath.Join(gopath, "src", "perkeep.org")
		validOpts = append(validOpts, validDir)
		fi2, err := os.Lstat(validDir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if os.SameFile(fi, fi2) {
			// In a valid directory.
			return nil
		}
	}
	if len(validOpts) == 1 {
		return fmt.Errorf("make.go cannot be run from %s; it must be in a valid GOPATH. Move the directory containing make.go to %s", dir, validOpts[0])
	} else {
		return fmt.Errorf("make.go cannot be run from %s; it must be in a valid GOPATH. Move the directory containing make.go to one of %q", dir, validOpts)
	}
}

const (
	goVersionMinor = 16
)

var validVersionRx = regexp.MustCompile(`go version go1\.(\d+)`)

// verifyGoModules ensures that "GO111MODULE" isn't set to "off"
func verifyGoModules() {
	gomodules := os.Getenv("GO11MODULE")
	if gomodules == "off" {
		log.Fatalf("GO11MODULE is set to 'off'. Please enable it to continue.")
	}
}

// verifyGoVersion runs "go version" and parses the output.  If the version is
// acceptable a check for gopherjs versions are also done. If problems
// are found a message is logged and we abort.
func verifyGoVersion() {
	_, err := exec.LookPath("go")
	if err != nil {
		log.Fatalf("Go doesn't appear to be installed ('go' isn't in your PATH). Install Go 1.%d or newer.", goVersionMinor)
	}
	out, err := exec.Command("go", "version").Output()
	if err != nil {
		log.Fatalf("Error checking Go version with the 'go' command: %v", err)
	}

	version := string(out)

	// Handle non-versioned binaries
	// ex: "go version devel +c26fac8 Thu Feb 15 21:41:39 2018 +0000 linux/amd64"
	if strings.HasPrefix(version, "go version devel ") {
		return
	}

	m := validVersionRx.FindStringSubmatch(version)
	if m == nil {
		log.Fatalf("Unexpected output while checking 'go version': %q", version)
	}
	minorVersion, err := strconv.Atoi(m[1])
	if err != nil {
		log.Fatalf("Unexpected error while parsing version string %q: %v", m[1], err)
	}

	if minorVersion < goVersionMinor {
		log.Fatalf("Your version of Go (%s) is too old. Perkeep requires Go 1.%d or later.", string(out), goVersionMinor)
	}

}

func doEmbed() {
	if *verbose {
		log.Printf("Embedding resources...")
	}
	closureEmbed := fullSrcPath("server/perkeepd/ui/closure/z_data.go")
	closureSrcDir := filepath.Join(pkRoot, filepath.FromSlash("clients/web/embed/closure/lib"))
	err := embedClosure(closureSrcDir, closureEmbed)
	if err != nil {
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
	fmt.Fprint(&qb, "// +build with_embed\n\n")
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

// hostExeName returns the executable name
// for s on the currently running host OS.
func hostExeName(s string) string {
	if runtime.GOOS == "windows" {
		return s + ".exe"
	}
	return s
}

// copied from pkg/osutil/paths.go
func homeDir() string {
	if runtime.GOOS == "windows" {
		return os.Getenv("HOMEDRIVE") + os.Getenv("HOMEPATH")
	}
	return os.Getenv("HOME")
}

func failIfCamlistoreOrgDir() {
	dir, _ := os.Getwd()
	if strings.HasSuffix(dir, "camlistore.org") {
		log.Fatalf(`Camlistore was renamed to Perkeep. Your current directory (%s) looks like a camlistore.org directory.

We're expecting you to be in a perkeep.org directory now.

See https://github.com/perkeep/perkeep/issues/981#issuecomment-354690313 for details.

You need to rename your "camlistore.org" parent directory to "perkeep.org"

`, dir)
	}
}
