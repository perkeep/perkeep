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
	"path/filepath"
	"runtime"
	"strings"
)

var (
	embedResources = flag.Bool("embed_static", true, "Whether to embed the closure library.")
	wantSQLite     = flag.Bool("sqlite", true, "Whether you want SQLite in your build. If you don't have any other database, you generally do.")
	all            = flag.Bool("all", false, "Force rebuild of everything (go install -a)")
	verbose        = flag.Bool("v", false, "Verbose mode")
)

func main() {
	log.SetFlags(0)
	flag.Parse()

	camRoot, err := os.Getwd()
	if err != nil {
		log.Fatalf("Failed to get current directory: %v", err)
	}
	verifyCamlistoreRoot(camRoot)
	verifyGoVersion()

	sql := haveSQLite()

	buildBaseDir := "build-gopath"
	if !sql {
		buildBaseDir += "-nosqlite"
	}

	// goPath becomes our child "go" processes' GOPATH environment variable:
	goPath := filepath.Join(camRoot, "tmp", buildBaseDir)
	binDir := filepath.Join(camRoot, "bin")

	// We copy all *.go files from camRoot's goDirs to buildSrcPath.
	goDirs := []string{"cmd", "pkg", "server/camlistored", "third_party"}

	buildSrcPath := filepath.Join(goPath, "src", "camlistore.org")

	if err := os.MkdirAll(buildSrcPath, 0755); err != nil {
		log.Fatal(err)
	}

	version := getVersion(camRoot)

	if *verbose {
		log.Printf("Camlistore version = %s", version)
		log.Printf("SQLite available: %v", sql)
		log.Printf("Temp GOPATH: %s", buildSrcPath)
		log.Printf("Output binaries: %s", binDir)
	}

	if !sql && *wantSQLite {
		log.Printf("SQLite not found. Either install it, or run make.go with --sqlite=false")
		switch runtime.GOOS {
		case "darwin":
			// TODO: search for /usr/local/Cellar/sqlite/*/lib/pkgconfig for the user.
			log.Printf("On OS X, run 'brew install sqlite3' and set PKG_CONFIG_PATH=/usr/local/Cellar/sqlite/3.7.17/lib/pkgconfig/")
		case "linux":
			log.Printf("On Linux, run 'sudo apt-get install libsqlite3-dev' or equivalent.")
		case "windows":
			log.Printf("On Windows, .... click stuff? TODO: fill this in")
		}
		os.Exit(2)
	}

	// Copy files we do want in our mirrored GOPATH.  This has the side effect of
	// populating wantDestFile, populated by mirrorFile.
	for _, dir := range goDirs {
		srcPath := filepath.Join(camRoot, filepath.FromSlash(dir))
		dstPath := filepath.Join(buildSrcPath, filepath.FromSlash(dir))
		if err := mirrorDir(srcPath, dstPath); err != nil {
			log.Fatalf("Error while mirroring %s to %s: %v", srcPath, dstPath, err)
		}
	}

	closureEmbed := filepath.Join(buildSrcPath, "server", "camlistored", "ui", "closure", "z_data.go")
	if *embedResources {
		closureSrcDir := filepath.Join(camRoot, "third_party", "closure", "lib")
		err := embedClosure(closureSrcDir, closureEmbed)
		if err != nil {
			log.Fatal(err)
		}
		wantDestFile[closureEmbed] = true
	}

	deleteUnwantedOldMirrorFiles(buildSrcPath)

	tags := ""
	if sql && *wantSQLite {
		tags = "with_sqlite"
	}
	args := []string{"install", "-v"}
	if *all {
		args = append(args, "-a")
	}
	args = append(args,
		"--ldflags=-X camlistore.org/pkg/buildinfo.GitInfo "+version,
		"--tags="+tags,
		"camlistore.org/cmd/camget",
		"camlistore.org/cmd/camput",
		"camlistore.org/cmd/camtool",
		"camlistore.org/server/camlistored",
		// Unnecessary, but to make sure we don't ship
		// anything or copy anything into the fake GOPATH
		// that's not needed or doesn't build:
		"camlistore.org/pkg/...",
		"camlistore.org/server/...",
		"camlistore.org/third_party/...",
	)
	switch runtime.GOOS {
	case "linux", "darwin":
		args = append(args, "camlistore.org/cmd/cammount")
	}
	cmd := exec.Command("go", args...)
	cmd.Env = append(cleanGoEnv(),
		"GOPATH="+goPath,
		"GOBIN="+binDir,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if *verbose {
		log.Printf("Running go with args %s", args)
	}
	if err := cmd.Run(); err != nil {
		log.Fatalf("Error building: %v", err)
	}
	log.Printf("Success. Binaries are in %s", binDir)
}

// cleanGoEnv returns a copy of the current environment with GOPATH and GOBIN removed.
func cleanGoEnv() (clean []string) {
	for _, env := range os.Environ() {
		if strings.HasPrefix(env, "GOPATH=") || strings.HasPrefix(env, "GOBIN=") {
			continue
		}
		clean = append(clean, env)
	}
	return
}

// getVersion returns the version of Camlistore. Either from a VERSION file at the root,
// or from git.
func getVersion(camRoot string) string {
	slurp, err := ioutil.ReadFile(filepath.Join(camRoot, "VERSION"))
	if err == nil {
		return strings.TrimSpace(string(slurp))
	}
	out, err := exec.Command(filepath.Join(camRoot, "misc", "gitversion")).Output()
	if err != nil {
		log.Fatalf("Error running ./misc/gitversion to determine Camlistore version: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// verifyCamlistoreRoot crashes if dir isn't the Camlistore root directory.
func verifyCamlistoreRoot(dir string) {
	testFile := filepath.Join(dir, "pkg", "blobref", "blobref.go")
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
		log.Fatalf("Your version of Go (%s) is too old. Camlistore requires Go 1.1 or later.")
	}
}

func mirrorDir(src, dst string) error {
	return filepath.Walk(src, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		base := fi.Name()
		if fi.IsDir() {
			if base == "testdata" || base == "genfileembed" ||
				strings.HasSuffix(path, "pkg/misc/closure/genclosuredeps") ||
				(base == "cmd" && strings.Contains(path, "github.com/camlistore/goexif")) {
				return filepath.SkipDir
			}
		}
		switch {
		case strings.HasSuffix(base, "_test.go"),
			strings.HasPrefix(base, ".#"),
			!strings.HasSuffix(base, ".go"):
			return nil
		}
		suffix, err := filepath.Rel(src, path)
		if err != nil {
			return fmt.Errorf("Failed to find Rel(%q, %q): %v", src, path, err)
		}
		return mirrorFile(path, filepath.Join(dst, suffix))
	})
}

var wantDestFile = make(map[string]bool) // full dest filename => true

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
		err = os.Chtimes(dst, sfi.ModTime(), sfi.ModTime())
	}
	return err
}

func deleteUnwantedOldMirrorFiles(dir string) {
	filepath.Walk(dir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			log.Fatalf("Error stating while cleaning %s: %v", path, err)
		}
		if fi.IsDir() {
			return nil
		}
		if !wantDestFile[path] {
			log.Printf("Deleting old file from temp build dir: %s", path)
			return os.Remove(path)
		}
		return nil
	})
}

func haveSQLite() bool {
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
		log.Fatalf("No pkg-config found. Can't determine whether sqlite3 is available, and where.")
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
	w := zip.NewWriter(&zipbuf)
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
		b, err := ioutil.ReadFile(path)
		if err != nil {
			return err
		}
		f, err := w.Create(suffix)
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
	fmt.Fprint(&qb, "func init() {\n\tZipData = ")
	quote(&qb, zipbuf.Bytes())
	fmt.Fprint(&qb, "\n}")

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
