//go:build ignore
// +build ignore

/*
Copyright 2015 The Perkeep Authors

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

// Command build-camlistore-server builds perkeepd and bundles all the
// necessary resources for a Perkeep server in docker. It should be run in a
// docker container.
package main // import "perkeep.org/misc/docker/server"

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
	"strings"
)

var (
	flagRev = flag.String("rev", "", "Perkeep revision to build (tag or commit hash). For development purposes, you can instead specify the path to a local Perkeep source tree from which to build, with the form \"WIP:/path/to/dir\".")
	outDir  = flag.String("outdir", "/OUT/", "Output directory, where perkeepd and all the resources will be written")
)

func usage() {
	fmt.Fprintf(os.Stderr, "Usage:\n")
	fmt.Fprintf(os.Stderr, "%s --rev=perkeep_revision\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "%s --rev=WIP:/path/to/camli/source/dir\n", os.Args[0])
	flag.PrintDefaults()
	example(os.Args[0])
	os.Exit(1)
}

func example(program string) {
	fmt.Fprintf(os.Stderr, "Examples:\n")
	fmt.Fprintf(os.Stderr, "\tdocker run --rm --volume=/tmp/camli-build/perkeep.org:/OUT perkeep/go %s --rev=4e8413c5012c\n", program)
	fmt.Fprintf(os.Stderr, "\tdocker run --rm --volume=/tmp/camli-build/perkeep.org:/OUT --volume=~/perkeep.org:/IN perkeep/go %s --rev=WIP:/IN\n", program)
}

func isWIP() bool {
	return strings.HasPrefix(*flagRev, "WIP")
}

// localCamliSource returns the path to the local Perkeep source tree
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

func getCamliSrc() {
	if localCamliSource() != "" {
		mirrorCamliSrc(localCamliSource())
	} else {
		fetchCamliSrc()
	}
	// we insert the version in the VERSION file, so make.go does no need git
	// in the container to detect the Perkeep version.
	check(os.Chdir("/gopath/src/perkeep.org"))
	check(ioutil.WriteFile("VERSION", []byte(rev()), 0777))
}

func mirrorCamliSrc(srcDir string) {
	check(os.MkdirAll("/gopath/src", 0777))
	cmd := exec.Command("cp", "-a", srcDir, "/gopath/src/perkeep.org")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("Error mirroring perkeep source from %v: %v", srcDir, err)
	}
}

func fetchCamliSrc() {
	check(os.MkdirAll("/gopath/src/perkeep.org", 0777))
	check(os.Chdir("/gopath/src/perkeep.org"))

	res, err := http.Get("https://api.github.com/repos/perkeep/perkeep/tarball/" + *flagRev)
	check(err)
	defer res.Body.Close()
	gz, err := gzip.NewReader(res.Body)
	check(err)
	defer gz.Close()
	tr := tar.NewReader(gz)
	var prefix string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		check(err)
		if strings.HasPrefix(h.Name, "pax_global_header") {
			continue
		}
		if prefix == "" {
			fields := strings.Split(h.Name, "/")
			prefix = fields[0] + "/"
		}
		name := strings.TrimPrefix(h.Name, prefix)
		if name == "" {
			continue
		}
		if h.Typeflag == tar.TypeDir {
			check(os.MkdirAll(name, os.FileMode(h.Mode)))
			continue
		}
		f, err := os.Create(name)
		check(err)
		n, err := io.Copy(f, tr)
		if err != nil && err != io.EOF {
			log.Fatal(err)
		}
		if n != h.Size {
			log.Fatalf("Error when creating %v: wanted %v bytes, got %v bytes", name, h.Size, n)
		}
		check(f.Close())
	}
}

func buildCamlistored() {
	oldPath := os.Getenv("PATH")
	os.Setenv("GOPATH", "/gopath")
	os.Setenv("PATH", "/usr/local/go/bin:"+oldPath)
	check(os.Chdir("/gopath/src/perkeep.org"))
	cmd := exec.Command("go", "run", "make.go",
		"-static", "true",
		"-targets", "perkeep.org/server/perkeepd")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("Error building perkeepd in go container: %v", err)
	}

	// And move it to the output dir
	check(os.MkdirAll(path.Join(*outDir, "/bin"), 0777))
	cmd = exec.Command("mv", "/gopath/bin/perkeepd", path.Join(*outDir, "/bin"))
	if err := cmd.Run(); err != nil {
		log.Fatalf("Error moving perkeepd binary %v in output dir %v: %v",
			"/gopath/src/perkeep.org/bin/perkeepd", path.Join(*outDir, "/bin"), err)
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
		if fields[2] == "/" {
			continue
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
	buildCamlistored()
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
