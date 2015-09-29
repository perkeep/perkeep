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
package main

import (
	"archive/tar"
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
)

var (
	rev      = flag.String("rev", "", "Camlistore revision to build (tag or commit hash)")
	localSrc = flag.String("camlisource", "", "(dev flag) Path to a local Camlistore source tree from which to build. It is ignored unless -rev=WORKINPROGRESS")
	outDir   = flag.String("outdir", "/OUT/", "Output directory, where the binaries will be written")
	buildOS  = flag.String("os", runtime.GOOS, "Operating system to build for.")
)

func usage() {
	fmt.Fprintf(os.Stderr, "Usage:\n")
	fmt.Fprintf(os.Stderr, "%s --rev=camlistore_revision\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "%s --rev=WORKINPROGRESS --camlisource=/path/to/camli/source/dir\n", os.Args[0])
	flag.PrintDefaults()
	example(os.Args[0])
	os.Exit(1)
}

func example(program string) {
	fmt.Fprintf(os.Stderr, "Examples:\n")
	fmt.Fprintf(os.Stderr, "\tdocker run --rm --volume=/tmp/camli-build/camlistore.org:/OUT camlistore/go %s --rev=4e8413c5012c\n", program)
	fmt.Fprintf(os.Stderr, "\tdocker run --rm --volume=/tmp/camli-build/camlistore.org:/OUT --volume=~/camlistore.org:/IN camlistore/go %s --rev=WORKINPROGRESS --camlisource=/IN\n", program)
}

func getCamliSrc() {
	if *localSrc != "" {
		mirrorCamliSrc(*localSrc)
	} else {
		fetchCamliSrc()
	}
	// if missing, we insert a VERSION FILE, so make.go does no need git in the container to detect the Camlistore version.
	check(os.Chdir("/gopath/src/camlistore.org"))
	if _, err := os.Stat("VERSION"); err != nil {
		if !os.IsNotExist(err) {
			log.Fatal(err)
		}
		check(ioutil.WriteFile("VERSION", []byte(*rev), 0777))
	}
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

	res, err := http.Get("https://camlistore.googlesource.com/camlistore/+archive/" + *rev + ".tar.gz")
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
	// Note: no need to set GO15VENDOREXPERIMENT because make.go does it.
	os.Setenv("GOPATH", "/gopath")
	os.Setenv("PATH", "/usr/local/go/bin:"+oldPath)
	cmd := exec.Command("go", "run", "make.go", "--use_gopath", "--os", *buildOS)
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
	if _, err := os.Stat("/.dockerinit"); err != nil {
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
