//go:build ignore

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
	"bytes"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

var (
	race         = flag.Bool("race", false, "Build race-detector version of binaries (they will run slowly)")
	verbose      = flag.Bool("v", strings.Contains(os.Getenv("CAMLI_DEBUG_X"), "makego"), "Verbose mode")
	targets      = flag.String("targets", "", "Optional comma-separated list of targets (i.e go packages) to build and install. '*' builds everything.  Empty builds defaults for this platform. Example: perkeep.org/server/perkeepd,perkeep.org/cmd/pk-put")
	quiet        = flag.Bool("quiet", false, "Don't print anything unless there's a failure.")
	buildARCH    = flag.String("arch", runtime.GOARCH, "Architecture to build for.")
	buildOS      = flag.String("os", runtime.GOOS, "Operating system to build for.")
	buildARM     = flag.String("arm", "7", "ARM version to use if building for ARM. Note that this version applies even if the host arch is ARM too (and possibly of a different version).")
	stampVersion = flag.Bool("stampversion", true, "Stamp version into buildinfo.GitInfo")
	website      = flag.Bool("website", false, "Just build the website.")
	static       = flag.Bool("static", false, "Build a static binary, so it can run in an empty container.")
	offline      = flag.Bool("offline", false, "Do not fetch the JS code for the web UI from perkeep.org. If not rebuilding the web UI, just trust the files on disk (if they exist).")
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
		"perkeep.org/server/perkeepd",
		"perkeep.org/app/hello",
		"perkeep.org/app/scanningcabinet",
		"perkeep.org/app/scanningcabinet/scancab",
	}
	switch *targets {
	case "*":
		buildAll = true
	case "":
		// Add pk-mount to default build targets on OSes that support FUSE.
		switch *buildOS {
		case "linux":
			targs = append(targs, "perkeep.org/cmd/pk-mount")
		}
	default:
		if *website {
			log.Fatal("--targets and --website are mutually exclusive")
		}
		if t := strings.Split(*targets, ","); len(t) != 0 {
			targs = t
		}
	}
	if *website {
		buildAll = false
		targs = []string{"perkeep.org/website/pk-web"}
	}

	tags := []string{"purego"} // for cznic/zappy
	if *static {
		tags = append(tags, "netgo", "osusergo")
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
	baseArgs = append(baseArgs, "--tags="+strings.Join(tags, ","))

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
	cmd.Env = goEnv()
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
	cmd.Env = goEnv()
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		log.Fatalf("Could not run go list to guess install dir: %v, %v", err, out)
	}
	return filepath.Dir(strings.TrimSpace(string(out)))
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

// Create an environment variable of the form key=value.
func envPair(key, value string) string {
	return fmt.Sprintf("%s=%s", key, value)
}

func goEnv() (ret []string) {
	ret = slices.Clone(os.Environ())
	var cross bool
	if *buildOS != runtime.GOOS {
		ret = append(ret, envPair("GOOS", *buildOS))
		cross = true
	}
	if *buildARCH != runtime.GOARCH {
		ret = append(ret, envPair("GOARCH", *buildARCH))
		cross = true
	}
	if cross {
		ret = append(ret, envPair("GOBIN", ""))
	}
	// If we're building for ARM (regardless of cross-compiling or not), we reset GOARM
	if *buildARCH == "arm" {
		ret = append(ret, envPair("GOARM", *buildARM))
	}
	return ret
}

// fullSrcPath returns the full path concatenation
// of pkRoot with fromSrc.
func fullSrcPath(fromSrc string) string {
	return filepath.Join(pkRoot, filepath.FromSlash(fromSrc))
}

// getVersion returns the version of Perkeep found in a VERSION file at the root.
func getVersion() string {
	slurp, err := os.ReadFile(filepath.Join(pkRoot, "VERSION"))
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
	cmd := exec.Command("go", "list", "-f", "{{.Target}}", "perkeep.org/cmd/pk")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		log.Fatalf("Could not run go list to find install dir: %v, %s", err, out)
	}
	binDir = filepath.Dir(strings.TrimSpace(string(out)))
}

const (
	goVersionMinor = 21
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
