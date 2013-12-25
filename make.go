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
	"bytes"
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
	embedResources = flag.Bool("embed_static", true, "Whether to embed the closure library.")
	sqlFlag        = flag.String("sqlite", "auto", "Whether you want SQLite in your build: yes, no, or auto.")
	all            = flag.Bool("all", false, "Force rebuild of everything (go install -a)")
	race           = flag.Bool("race", false, "Build race-detector version of binaries (they will run slowly)")
	verbose        = flag.Bool("v", false, "Verbose mode")
	targets        = flag.String("targets", "", "Optional comma-separated list of targets (i.e go packages) to build and install. Empty means all. Example: camlistore.org/server/camlistored,camlistore.org/cmd/camput")
	quiet          = flag.Bool("quiet", false, "Don't print anything unless there's a failure.")
	onlysync       = flag.Bool("onlysync", false, "Only populate the temporary source/build tree and output its full path. It is meant to prepare the environment for running the full test suite with 'devcam test'.")
	// TODO(mpl): looks like ifModsSince is not used anywhere?
	ifModsSince = flag.Int64("if_mods_since", 0, "If non-zero return immediately without building if there aren't any filesystem modifications past this time (in unix seconds)")
	buildARCH   = flag.String("arch", runtime.GOARCH, "Architecture to build for.")
	buildOS     = flag.String("os", runtime.GOOS, "Operating system to build for.")
)

var (
	// buildGoPath becomes our child "go" processes' GOPATH environment variable
	buildGoPath string
	// Our temporary source tree root and build dir, i.e: buildGoPath + "src/camlistore.org"
	buildSrcDir string
	// files mirrored from camRoot to buildSrcDir
	rxMirrored = regexp.MustCompile(`^([a-zA-Z0-9\-\_]+\.(?:go|html|js|css|png|jpg|gif|ico|gpg|json|err|camli|svg))$`)
)

func main() {
	log.SetFlags(0)
	flag.Parse()

	camRoot, err := os.Getwd()
	if err != nil {
		log.Fatalf("Failed to get current directory: %v", err)
	}
	verifyCamlistoreRoot(camRoot)

	cross := runtime.GOOS != *buildOS || runtime.GOARCH != *buildARCH
	var sql bool
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
			log.Printf("SQLite is not easy on windows. Please see http://camlistore.org/docs/server-config#windows")
		}
		os.Exit(2)
	}

	buildBaseDir := "build-gopath"
	if !sql {
		buildBaseDir += "-nosqlite"
	}

	buildGoPath = filepath.Join(camRoot, "tmp", buildBaseDir)
	binDir := filepath.Join(camRoot, "bin")
	buildSrcDir = filepath.Join(buildGoPath, "src", "camlistore.org")

	if err := os.MkdirAll(buildSrcDir, 0755); err != nil {
		log.Fatal(err)
	}

	version := getVersion(camRoot)

	if *verbose {
		log.Printf("Camlistore version = %s", version)
		log.Printf("SQLite included: %v", sql)
		log.Printf("Temporary source: %s", buildSrcDir)
		log.Printf("Output binaries: %s", binDir)
	}

	// TODO(mpl): main is getting long. We could probably move all the mirroring
	// dance to its own func.
	// We copy all *.go files from camRoot's goDirs to buildSrcDir.
	goDirs := []string{"cmd", "pkg", "dev", "server/camlistored", "third_party"}
	if *onlysync {
		goDirs = append(goDirs, "server/appengine", "config")
	}
	// Copy files we do want in our mirrored GOPATH.  This has the side effect of
	// populating wantDestFile, populated by mirrorFile.
	var latestSrcMod time.Time
	for _, dir := range goDirs {
		oriPath := filepath.Join(camRoot, filepath.FromSlash(dir))
		dstPath := buildSrcPath(dir)
		if maxMod, err := mirrorDir(oriPath, dstPath, mirrorOpts{sqlite: sql}); err != nil {
			log.Fatalf("Error while mirroring %s to %s: %v", oriPath, dstPath, err)
		} else {
			if maxMod.After(latestSrcMod) {
				latestSrcMod = maxMod
			}
		}
	}

	verifyGoVersion()

	if *onlysync {
		mirrorFile("make.go", filepath.Join(buildSrcDir, "make.go"))
		deleteUnwantedOldMirrorFiles(buildSrcDir, true)
		fmt.Println(buildGoPath)
		return
	}

	buildAll := true
	targs := []string{
		"camlistore.org/dev/devcam",
		"camlistore.org/cmd/camget",
		"camlistore.org/cmd/camput",
		"camlistore.org/cmd/camtool",
		"camlistore.org/server/camlistored",
	}
	if *targets != "" {
		if t := strings.Split(*targets, ","); len(t) != 0 {
			targs = t
			buildAll = false
		}
	}

	withCamlistored := stringListContains(targs, "camlistore.org/server/camlistored")
	if *embedResources && withCamlistored {
		if *verbose {
			log.Printf("Embedding resources...")
		}
		closureEmbed := buildSrcPath("server/camlistored/ui/closure/z_data.go")
		closureSrcDir := filepath.Join(camRoot, filepath.FromSlash("third_party/closure/lib"))
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

	deleteUnwantedOldMirrorFiles(buildSrcDir, withCamlistored)

	tags := ""
	if sql {
		tags = "with_sqlite"
	}
	baseArgs := []string{"install", "-v"}
	if *all {
		baseArgs = append(baseArgs, "-a")
	}
	if *race {
		baseArgs = append(baseArgs, "-race")
	}
	baseArgs = append(baseArgs,
		"--ldflags=-X camlistore.org/pkg/buildinfo.GitInfo "+version,
		"--tags="+tags)

	if buildAll {
		switch *buildOS {
		case "linux", "darwin":
			targs = append(targs, "camlistore.org/cmd/cammount")
		}
	}

	// First install command: build just the final binaries, installed to a GOBIN
	// under <camlistore_root>/bin:
	args := append(baseArgs, targs...)

	if buildAll {
		args = append(args,
			"camlistore.org/pkg/...",
			"camlistore.org/server/...",
			"camlistore.org/third_party/...",
		)
	}

	cmd := exec.Command("go", args...)
	cmd.Env = append(cleanGoEnv(),
		"GOPATH="+buildGoPath,
	)
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
		clean = append(clean, env)
	}
	if *buildOS != runtime.GOOS {
		clean = append(clean, envPair("GOOS", *buildOS))
	}
	if *buildARCH != runtime.GOARCH {
		clean = append(clean, envPair("GOARCH", *buildARCH))
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
	cmdName := filepath.Join(buildGoPath, "bin", "genfileembed")
	uiEmbeds := buildSrcPath("server/camlistored/ui")
	serverEmbeds := buildSrcPath("pkg/server")
	for _, embeds := range []string{uiEmbeds, serverEmbeds} {
		args := []string{embeds}
		cmd := exec.Command(cmdName, args...)
		cmd.Env = append(cleanGoEnv(),
			"GOPATH="+buildGoPath,
		)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if *verbose {
			log.Printf("Running %s %s", cmdName, embeds)
		}
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("Error running %s %s: %v", cmdName, embeds, err)
		}
		// We mark all the zembeds in builddir as wanted, so that we do not
		// have to regen them next time, unless they need updating.
		f, err := os.Open(embeds)
		if err != nil {
			return err
		}
		defer f.Close()
		names, err := f.Readdirnames(-1)
		if err != nil {
			return err
		}
		for _, v := range names {
			if strings.HasPrefix(v, "zembed_") {
				wantDestFile[filepath.Join(embeds, v)] = true
			}
		}
	}
	return nil
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
func getVersion(camRoot string) string {
	slurp, err := ioutil.ReadFile(filepath.Join(camRoot, "VERSION"))
	if err == nil {
		return strings.TrimSpace(string(slurp))
	}
	return gitVersion(camRoot)
}

var gitVersionRx = regexp.MustCompile(`\b\d\d\d\d-\d\d-\d\d-[0-9a-f]{7,7}\b`)

// gitVersion returns the git version of the git repo at camRoot as a
// string of the form "yyyy-mm-dd-xxxxxxx", with an optional trailing
// '+' if there are any local uncomitted modifications to the tree.
func gitVersion(camRoot string) string {
	cmd := exec.Command("git", "rev-list", "--max-count=1", "--pretty=format:'%ad-%h'", "--date=short", "HEAD")
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

func verifyGoVersion() {
	_, err := exec.LookPath("go")
	if err != nil {
		log.Fatalf("Go doesn't appeared to be installed ('go' isn't in your PATH). Install Go 1.1 or newer.")
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
	switch version {
	case "go1", "go1.0.1", "go1.0.2", "go1.0.3":
		log.Fatalf("Your version of Go (%s) is too old. Camlistore requires Go 1.1 or later.", version)
	}
}

type mirrorOpts struct {
	sqlite bool // want sqlite package?
}

func mirrorDir(src, dst string, opts mirrorOpts) (maxMod time.Time, err error) {
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
		if strings.HasPrefix(base, ".#") || !rxMirrored.MatchString(base) {
			return nil
		}
		suffix, err := filepath.Rel(src, path)
		if err != nil {
			return fmt.Errorf("Failed to find Rel(%q, %q): %v", src, path, err)
		}
		if t := fi.ModTime(); t.After(maxMod) {
			maxMod = t
		}
		return mirrorFile(path, filepath.Join(dst, suffix))
	})
	return
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
		if !wantDestFile[path] {
			if !withCamlistored && (strings.Contains(path, "zembed_") || strings.Contains(path, "z_data.go")) {
				// If we're not building the camlistored binary,
				// no need to clean up the embedded Closure, JS,
				// CSS, HTML, etc. Doing so would just mean we'd
				// have to put it back into place later.
				return nil
			}
			if !*quiet {
				log.Printf("Deleting old file from temp build dir: %s", path)
			}
			return os.Remove(path)
		}
		return nil
	})
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

func embedClosure(closureDir, embedFile string) error {
	if _, err := os.Stat(closureDir); err != nil {
		return fmt.Errorf("Could not stat %v: %v", closureDir, err)
	}

	// first, zip it
	var zipbuf bytes.Buffer
	var zipdest io.Writer = &zipbuf
	if os.Getenv("CAMLI_WRITE_TMP_ZIP") != "" {
		f, _ := os.Create("/tmp/camli-closure.zip")
		zipdest = io.MultiWriter(zipdest, f)
		defer f.Close()
	}
	var modTime time.Time
	w := zip.NewWriter(zipdest)
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
		b, err := ioutil.ReadFile(path)
		if err != nil {
			return err
		}
		f, err := w.Create(filepath.ToSlash(suffix))
		if err != nil {
			log.Fatal(err)
		}
		_, err = f.Write(b)
		return err
	})
	if err != nil {
		return err
	}
	err = w.Close()
	if err != nil {
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
	// TODO(mpl): do not regenerate the whole zip file if the modtime
	// of the z_data.go file is greater than the modtime of all the closure *.js files.
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
	if err != nil {
		return false
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
