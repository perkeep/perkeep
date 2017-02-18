// +build ignore

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

// Command build-binaries builds camlistored and the Camlistore tools.
// It should be run in a docker container.
package main // import "camlistore.org/misc/docker/release"

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strings"
)

var (
	flagRev     = flag.String("rev", "", "Camlistore revision to build (tag or commit hash). For development purposes, you can instead specify the path to a local Camlistore source tree from which to build, with the form \"WIP:/path/to/dir\".")
	flagVersion = flag.String("version", "", "The optional version number (e.g. 0.9) that will be stamped into the binaries, in addition to the revision.")
	outDir      = flag.String("outdir", "/OUT/", "Output directory, where the binaries will be written")
	buildOS     = flag.String("os", runtime.GOOS, "Operating system to build for.")
)

func usage() {
	fmt.Fprintf(os.Stderr, "Usage:\n")
	fmt.Fprintf(os.Stderr, "%s --rev=camlistore_revision\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "%s --rev=WIP:/path/to/camli/source/dir\n", os.Args[0])
	flag.PrintDefaults()
	example(os.Args[0])
	os.Exit(1)
}

func example(program string) {
	fmt.Fprintf(os.Stderr, "Examples:\n")
	fmt.Fprintf(os.Stderr, "\tdocker run --rm --volume=/tmp/camli-build/camlistore.org:/OUT camlistore/go %s --rev=4e8413c5012c\n", program)
	fmt.Fprintf(os.Stderr, "\tdocker run --rm --volume=/tmp/camli-build/camlistore.org:/OUT --volume=~/camlistore.org:/IN camlistore/go %s --rev=WIP:/IN\n", program)
}

func isWIP() bool {
	return strings.HasPrefix(*flagRev, "WIP")
}

// localCamliSource returns the path to the local Camlistore source tree
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

func version() string {
	// TODO(mpl): cut the rev to 10 chars, like make.go would do?
	if *flagVersion != "" {
		return fmt.Sprintf("%v (git rev %v)", *flagVersion, rev())
	}
	return rev()
}

func getCamliSrc() {
	if localCamliSource() != "" {
		mirrorCamliSrc(localCamliSource())
	} else {
		fetchCamliSrc()
	}
	// we insert the version in the VERSION file, so make.go does no need git
	// in the container to detect the Camlistore version.
	check(os.Chdir("/gopath/src/camlistore.org"))
	check(ioutil.WriteFile("VERSION", []byte(version()), 0777))
}

func mirrorCamliSrc(srcDir string) {
	check(os.MkdirAll("/gopath/src", 0777))
	cmd := exec.Command("cp", "-a", srcDir, "/gopath/src/camlistore.org")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("Error mirroring camlistore source from %v: %v", srcDir, err)
	}
}

func fetchCamliSrc() {
	check(os.MkdirAll("/gopath/src/camlistore.org", 0777))
	check(os.Chdir("/gopath/src/camlistore.org"))

	res, err := http.Get("https://camlistore.googlesource.com/camlistore/+archive/" + *flagRev + ".tar.gz")
	check(err)
	defer res.Body.Close()
	gz, err := gzip.NewReader(res.Body)
	check(err)
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		check(err)
		if h.Typeflag == tar.TypeDir {
			check(os.MkdirAll(h.Name, os.FileMode(h.Mode)))
			continue
		}
		f, err := os.Create(h.Name)
		check(err)
		n, err := io.Copy(f, tr)
		if err != nil && err != io.EOF {
			log.Fatal(err)
		}
		if n != h.Size {
			log.Fatalf("Error when creating %v: wanted %v bytes, got %v bytes", h.Name, h.Size, n)
		}
		check(f.Close())
	}
}

func build() {
	check(os.Chdir("/gopath/src/camlistore.org"))
	oldPath := os.Getenv("PATH")
	os.Setenv("GOPATH", "/gopath")
	os.Setenv("PATH", "/usr/local/go/bin:"+oldPath)
	check(os.Setenv("CAMLI_GOPHERJS_GOROOT", "/usr/local/go"))
	cmd := exec.Command("go", "run", "make.go", "--os", *buildOS)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("Error building all Camlistore binaries for %v in go container: %v", *buildOS, err)
	}
	srcDir := "bin"
	if *buildOS != "linux" {
		// TODO(mpl): probably bail early if GOARCH != amd64. Or do we want to distribute for other arches?
		srcDir = path.Join(srcDir, *buildOS+"_amd64")
	}
	cmd = exec.Command("mv", srcDir, path.Join(*outDir, "/bin"))
	if err := cmd.Run(); err != nil {
		log.Fatalf("Error copying Camlistore binaries to %v: %v", path.Join(*outDir, "/bin"), err)
	}
}

func checkArgs() {
	if flag.NArg() != 0 {
		usage()
	}
	if *flagRev == "" {
		fmt.Fprintf(os.Stderr, "Usage error: --rev is required.\n")
		usage()
	}
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

func main() {
	flag.Usage = usage
	flag.Parse()
	if !inDocker() {
		fmt.Fprintf(os.Stderr, "Usage error: this program should be run within a docker container, and is meant to be called from misc/docker/dock.go\n")
		usage()
	}
	checkArgs()

	getCamliSrc()
	build()
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
