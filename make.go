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
// The output binaries go into the ./bin/ directory (under the
// Perkeep root, where make.go is)
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
	race           = flag.Bool("race", false, "Build race-detector version of binaries (they will run slowly)")
	verbose        = flag.Bool("v", strings.Contains(os.Getenv("CAMLI_DEBUG_X"), "makego"), "Verbose mode")
	targets        = flag.String("targets", "", "Optional comma-separated list of targets (i.e go packages) to build and install. '*' builds everything.  Empty builds defaults for this platform. Example: perkeep.org/server/perkeepd,perkeep.org/cmd/pk-put")
	quiet          = flag.Bool("quiet", false, "Don't print anything unless there's a failure.")
	buildARCH      = flag.String("arch", runtime.GOARCH, "Architecture to build for.")
	buildOS        = flag.String("os", runtime.GOOS, "Operating system to build for.")
	buildARM       = flag.String("arm", "7", "ARM version to use if building for ARM. Note that this version applies even if the host arch is ARM too (and possibly of a different version).")
	stampVersion   = flag.Bool("stampversion", true, "Stamp version into buildinfo.GitInfo")
	website        = flag.Bool("website", false, "Just build the website.")
	camnetdns      = flag.Bool("camnetdns", false, "Just build perkeep.org/server/camnetdns.")
	static         = flag.Bool("static", false, "Build a static binary, so it can run in an empty container.")
	skipGopherJS   = flag.Bool("skip_gopherjs", false, "skip building/running GopherJS, even if building perkeepd/etc")
)

var (
	// pkRoot is the Perkeep project root
	pkRoot string
	binDir string // $GOBIN or $GOPATH/bin, based on user setting or default Go value.

	// gopherjsGoroot should be specified through the env var
	// CAMLI_GOPHERJS_GOROOT when the user's using go tip, because gopherjs only
	// builds with Go 1.10.
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

	failIfCamlistoreOrgDir()
	verifyGoVersion()
	verifyPerkeepRoot()
	version := getVersion()
	gitRev := getGitVersion()
	sql := withSQLite()

	if *verbose {
		log.Printf("Perkeep version = %q, git = %q", version, gitRev)
		log.Printf("SQLite included: %v", sql)
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

	withPerkeepd := stringListContains(targs, "perkeep.org/server/perkeepd")
	withPublisher := stringListContains(targs, "perkeep.org/app/publisher")
	if (withPerkeepd || withPublisher) && !*skipGopherJS {
		if err := buildReactGen(); err != nil {
			log.Fatal(err)
		}
		if withPerkeepd {
			if err := genWebUIReact(); err != nil {
				log.Fatal(err)
			}
		}
		// gopherjs has to run before doEmbed since we need all the javascript
		// to be generated before embedding happens.
		if err := makeJS(withPerkeepd, withPublisher); err != nil {
			log.Fatal(err)
		}
	}

	if *embedResources && withPerkeepd {
		doEmbed()
	}

	tags := []string{"purego"} // for cznic/zappy
	if *static {
		tags = append(tags, "netgo")
	}
	if sql {
		// used by go-sqlite to use system sqlite libraries
		tags = append(tags, "libsqlite3")
		// used by perkeep to switch behavior to sqlite for tests
		// and some underlying libraries
		tags = append(tags, "with_sqlite")
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
	gopherjsUI  = "server/perkeepd/ui/goui.js"
)

func buildGopherjs() error {
	// if gopherjs binary already exists, record its modtime, so we can reset it later.
	// See explanation below.
	outBin := hostExeName(filepath.Join(binDir, "gopherjs"))
	fi, err := os.Stat(outBin)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	modtime := time.Now()
	var hashBefore string
	if err == nil {
		modtime = fi.ModTime()
		hashBefore = hashsum(outBin)
	}

	src := filepath.Join(pkRoot, filepath.FromSlash("vendor/github.com/gopherjs/gopherjs"))
	goBin := "go"
	if gopherjsGoroot != "" {
		goBin = hostExeName(filepath.Join(gopherjsGoroot, "bin", "go"))
	}
	cmd := exec.Command(goBin, "install", "-v")
	cmd.Dir = src
	cmd.Env = os.Environ()
	// forcing GOOS and GOARCH to prevent cross-compiling, as gopherjs will run on the
	// current (host) platform.
	cmd.Env = append(cmd.Env, "GOOS="+runtime.GOOS)
	cmd.Env = append(cmd.Env, "GOARCH="+runtime.GOARCH)
	if gopherjsGoroot != "" {
		cmd.Env = append(cmd.Env, "GOROOT="+gopherjsGoroot)
	}
	var buf bytes.Buffer
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("error while building gopherjs: %v, %v", err, buf.String())
	}
	if *verbose {
		fmt.Println(buf.String())
	}

	hashAfter := hashsum(outBin)
	if hashAfter != hashBefore {
		log.Printf("gopherjs rebuilt at %v", outBin)
		return nil
	}
	// even if the source hasn't changed, apparently goinstall still at least bumps
	// the modtime. Which means, 'gopherjs install' would then always rebuild its
	// output too, even if no source changed since last time. We want to avoid that
	// (because then parts of Perkeep get unnecessarily rebuilt too and yada yada), so
	// we reset the modtime of gopherjs if the binary is the same as the previous time
	// it was built.
	return os.Chtimes(outBin, modtime, modtime)
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
	cmd := exec.Command("go", "generate", "-v", "perkeep.org/app/publisher/js")
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

func genWebUIJS() error {
	output := filepath.Join(pkRoot, filepath.FromSlash(gopherjsUI))
	pkg := "perkeep.org/server/perkeepd/ui/goui"
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
	gopherjsBin := hostExeName(filepath.Join(binDir, "gopherjs"))
	args := []string{"install", pkg, "-v", "--tags", "nocgo noReactBundle"}
	if *embedResources {
		// when embedding for "production", use -m to minify the javascript output
		args = append(args, "-m")
	}
	cmd := exec.Command(gopherjsBin, args...)
	cmd.Env = os.Environ()
	// Pretend we're on linux regardless of the actual host, because recommended
	// hack to work around https://github.com/gopherjs/gopherjs/issues/511
	cmd.Env = append(cmd.Env, "GOOS=linux")
	if gopherjsGoroot != "" {
		cmd.Env = append(cmd.Env, "GOROOT="+gopherjsGoroot)
	}
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

// genWebUIReact runs go generate on the gopherjs code of the web UI, which
// invokes reactGen on the Go React components. This generates the boilerplate
// code, in gen_*_reactGen.go files, required to complete those components.
func genWebUIReact() error {
	args := []string{"generate", "-v", "perkeep.org/server/perkeepd/ui/goui/..."}

	path := strings.Join([]string{
		binDir,
		os.Getenv("PATH"),
	}, string(os.PathListSeparator))

	cmd := exec.Command("go", args...)
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, "PATH="+path)
	var buf bytes.Buffer
	cmd.Stderr = &buf
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("go generate for web UI error: %v, %v", err, buf.String())
	}
	if *verbose {
		fmt.Println(buf.String())
	}
	return nil
}

// makeJS builds and runs the gopherjs command on perkeep.org/app/publisher/js
// and perkeep.org/server/perkeepd/ui/goui
func makeJS(doWebUI, doPublisher bool) error {
	if err := buildGopherjs(); err != nil {
		return fmt.Errorf("error building gopherjs: %v", err)
	}

	if doPublisher {
		if err := genPublisherJS(); err != nil {
			return err
		}
	}
	if doWebUI {
		if err := genWebUIJS(); err != nil {
			return err
		}
	}
	return nil
}

// Create an environment variable of the form key=value.
func envPair(key, value string) string {
	return fmt.Sprintf("%s=%s", key, value)
}

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

func genEmbeds() error {
	cmdName := hostExeName(filepath.Join(binDir, "genfileembed"))
	for _, embeds := range []string{
		"server/perkeepd/ui",
		"pkg/server",
		"clients/web/embed/fontawesome",
		"clients/web/embed/glitch",
		"clients/web/embed/leaflet",
		"clients/web/embed/less",
		"clients/web/embed/opensans",
		"clients/web/embed/react",
		"app/publisher",
		"app/scanningcabinet/ui",
	} {
		embeds := fullSrcPath(embeds)
		args := []string{"-build-tags=with_embed"}
		args = append(args, embeds)
		cmd := exec.Command(cmdName, args...)
		cmd.Stdout = os.Stdout
		var buf bytes.Buffer
		cmd.Stderr = &buf

		if *verbose {
			log.Printf("Running %s %s", cmdName, embeds)
		}
		if err := cmd.Run(); err != nil {
			os.Stderr.Write(buf.Bytes())
			return fmt.Errorf("error running %s %s: %v", cmdName, embeds, err)
		}
		if *verbose {
			fmt.Println(buf.String())
		}
	}
	return nil
}

func buildGenfileembed() error {
	return buildBin("perkeep.org/pkg/fileembed/genfileembed")
}

func buildReactGen() error {
	return buildBin("perkeep.org/vendor/myitcv.io/react/cmd/reactGen")
}

func buildDevcam() error {
	return buildBin("perkeep.org/dev/devcam")
}

func buildBin(pkg string) error {
	pkgBase := pathpkg.Base(pkg)

	args := []string{"install", "-v"}
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
	validateDirInGOPATH(pkRoot)

	cmd := exec.Command("go", "list", "-f", "{{.Target}}", "perkeep.org/cmd/pk")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		log.Fatalf("Could not run go list to find install dir: %v, %s", err, out)
	}
	binDir = filepath.Dir(strings.TrimSpace(string(out)))
}

func validateDirInGOPATH(dir string) {
	fi, err := os.Lstat(dir)
	if err != nil {
		log.Fatal(err)
	}

	gopathEnv, err := exec.Command("go", "env", "GOPATH").Output()
	if err != nil {
		log.Fatalf("error finding GOPATH: %v", err)
	}
	gopaths := filepath.SplitList(strings.TrimSpace(string(gopathEnv)))
	if len(gopaths) == 0 {
		log.Fatalf("failed to find your GOPATH: go env GOPATH returned nothing")
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
			log.Fatal(err)
		}
		if os.SameFile(fi, fi2) {
			// In a valid directory.
			return
		}
	}
	if len(validOpts) == 1 {
		log.Fatalf("make.go cannot be run from %s; it must be in a valid GOPATH. Move the directory containing make.go to %s", dir, validOpts[0])
	} else {
		log.Fatalf("make.go cannot be run from %s; it must be in a valid GOPATH. Move the directory containing make.go to one of %q", dir, validOpts)
	}
}

const (
	goVersionMinor  = 10
	gopherJSGoMinor = 10
)

var validVersionRx = regexp.MustCompile(`go version go1\.(\d+)`)

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
		verifyGopherjsGoroot(" devel")
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

	if minorVersion != gopherJSGoMinor {
		verifyGopherjsGoroot(fmt.Sprintf("1.%d", minorVersion))
	}
}

func verifyGopherjsGoroot(goFound string) {
	gopherjsGoroot = os.Getenv("CAMLI_GOPHERJS_GOROOT")
	goBin := hostExeName(filepath.Join(gopherjsGoroot, "bin", "go"))
	if gopherjsGoroot == "" {
		goInHomeDir, err := findGopherJSGoroot()
		if err != nil {
			log.Fatalf("Error while looking for a go1.%d dir in %v: %v", gopherJSGoMinor, homeDir(), err)
		}
		if goInHomeDir == "" {
			log.Fatalf("You're using go%s != go1.%d, which GopherJS requires, and it was not found in %v. You need to specify a go1.%d root in CAMLI_GOPHERJS_GOROOT for building GopherJS.", goFound, gopherJSGoMinor, homeDir(), gopherJSGoMinor)
		}
		gopherjsGoroot = filepath.Join(homeDir(), goInHomeDir)
		goBin = hostExeName(filepath.Join(gopherjsGoroot, "bin", "go"))
		log.Printf("You're using go%s != go1.%d, which GopherJS requires, and CAMLI_GOPHERJS_GOROOT was not provided, so defaulting to %v for building GopherJS instead.", goFound, gopherJSGoMinor, goBin)
	}
	if _, err := os.Stat(goBin); err != nil {
		if !os.IsNotExist(err) {
			log.Fatal(err)
		}
		log.Fatalf("%v not found. You need to specify a go1.%d root in CAMLI_GOPHERJS_GOROOT for building GopherJS", goBin, gopherJSGoMinor)
	}
}

// findGopherJSGoroot tries to find a go1.gopherJSGoMinor.* go root in the home
// directory. It returns the empty string and no error if none was found.
func findGopherJSGoroot() (string, error) {
	dir, err := os.Open(homeDir())
	if err != nil {
		return "", err
	}
	defer dir.Close()
	names, err := dir.Readdirnames(-1)
	if err != nil {
		return "", err
	}
	goVersion := fmt.Sprintf("go1.%d", gopherJSGoMinor)
	for _, name := range names {
		if strings.HasPrefix(name, goVersion) {
			return name, nil
		}
	}
	return "", nil
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
		// TODO(lindner): fix these docs.
		log.Printf("SQLite not found. Either install it, or run make.go with --sqlite=false  See https://code.google.com/p/camlistore/wiki/SQLite")
		switch runtime.GOOS {
		case "darwin":
			log.Printf("On OS X, run 'brew install sqlite3 pkg-config'. Get brew from http://mxcl.github.io/homebrew/")
		case "linux":
			log.Printf("On Linux, run 'sudo apt-get install libsqlite3-dev' or equivalent.")
		case "windows":
			log.Printf("SQLite is not easy on windows. Please see https://perkeep.org/doc/server-config#windows")
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
	closureEmbed := fullSrcPath("server/perkeepd/ui/closure/z_data.go")
	closureSrcDir := filepath.Join(pkRoot, filepath.FromSlash("clients/web/embed/closure/lib"))
	err := embedClosure(closureSrcDir, closureEmbed)
	if err != nil {
		log.Fatal(err)
	}
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
