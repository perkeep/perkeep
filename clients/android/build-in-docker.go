// +build ignore

/*
Copyright 2017 The Camlistore Authors.

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

// This program builds the Camlistore Android application. It is meant to be run
// within the relevant docker container.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var flagRelease = flag.Bool("release", false, "Whether to assemble the release build, instead of the debug build.")

// TODO(mpl): not sure if the version in app/build.gradle should have anything
// to do with the version we want to use here. look into that later.
const appVersion = "0.7"

var (
	camliDir   = filepath.Join(os.Getenv("GOPATH"), "src/camlistore.org")
	projectDir = filepath.Join(os.Getenv("GOPATH"), "src/camlistore.org/clients/android")
	camputBin  = filepath.Join(projectDir, "app/build/generated/assets/camput.arm")
	assetsDir  = filepath.Join(projectDir, "app/src/main/assets")
)

func main() {
	flag.Parse()
	if !inDocker() {
		fmt.Fprintf(os.Stderr, "Usage error: this program should be run within a docker container\n")
		os.Exit(2)
	}
	buildCamput()
	writeVersion()
	buildApp()
}

func buildApp() {
	cmd := exec.Command("./gradlew", "assembleDebug")
	if *flagRelease {
		cmd = exec.Command("./gradlew", "assembleRelease")
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("Error building Android app: %v", err)
	}
}

func writeVersion() {
	if err := ioutil.WriteFile(filepath.Join(assetsDir, "camput-version.txt"), []byte(version()), 0600); err != nil {
		log.Fatalf("Error writing app version file: %v", err)
	}
}

func buildCamput() {
	os.Setenv("GOARCH", "arm")
	os.Setenv("GOARM", "7")
	cmd := exec.Command("go", "build", "-o", camputBin, "camlistore.org/cmd/camput")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("Error building camput for Android: %v", err)
	}

	if err := os.Rename(camputBin, filepath.Join(assetsDir, "camput.arm")); err != nil {
		log.Fatalf("Error moving camput to assets dir: %v", err)
	}
}

func version() string {
	return "app " + appVersion + " camput " + getVersion() + " " + goVersion()
}

func goVersion() string {
	out, err := exec.Command("go", "version").Output()
	if err != nil {
		log.Fatalf("Error getting Go version with the 'go' command: %v", err)
	}
	return string(out)
}

// getVersion returns the version of Camlistore. Either from a VERSION file at the root,
// or from git.
func getVersion() string {
	slurp, err := ioutil.ReadFile(filepath.Join(camliDir, "VERSION"))
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
	cmd.Dir = camliDir
	out, err := cmd.Output()
	if err != nil {
		log.Fatalf("Error running git rev-list in %s: %v", camliDir, err)
	}
	v := strings.TrimSpace(string(out))
	if m := gitVersionRx.FindStringSubmatch(v); m != nil {
		v = m[0]
	} else {
		panic("Failed to find git version in " + v)
	}
	cmd = exec.Command("git", "diff", "--exit-code")
	cmd.Dir = camliDir
	if err := cmd.Run(); err != nil {
		v += "+"
	}
	return v
}

func inDocker() bool {
	r, err := os.Open("/proc/self/cgroup")
	if err != nil {
		log.Fatalf(`can't open "/proc/self/cgroup": %v`, err)
	}
	defer r.Close()
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		l := sc.Text()
		fields := strings.SplitN(l, ":", 3)
		if len(fields) != 3 {
			log.Fatal(`unexpected line in "/proc/self/cgroup"`)
		}
		if !strings.HasPrefix(fields[2], "/docker/") {
			return false
		}
	}
	if err := sc.Err(); err != nil {
		log.Fatal(err)
	}
	return true
}
