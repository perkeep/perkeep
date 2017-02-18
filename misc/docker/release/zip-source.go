// +build ignore

/*
Copyright 2016 The Camlistore Authors

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

// Command zip-source packs the Camlistore source in a zip file, for a release.
// It should be run in a docker container.
package main

import (
	"archive/tar"
	"archive/zip"
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
	"path/filepath"
	"strings"
)

var (
	flagRev     = flag.String("rev", "", "Camlistore revision to ship (tag or commit hash). For development purposes, you can instead specify the path to a local Camlistore source tree from which to build, with the form \"WIP:/path/to/dir\".")
	flagVersion = flag.String("version", "", "The version number that is used in the zip file name, and in the VERSION file, e.g. 0.10")
	flagOutDir  = flag.String("outdir", "/OUT/", "Directory where to write the zip file.")
	flagSanity  = flag.Bool("sanity", true, "Check before making the zip that its contents pass the \"go run make.go\" test.")
)

const tmpSource = "/tmp/camlistore.org"

var (
	// Everything that should be included in the release.
	// maps filename to whether it's a directory.
	rootNames = map[string]bool{
		"app":             true,
		"AUTHORS":         false,
		"bin":             true,
		"BUILDING":        false,
		"clients":         true,
		"cmd":             true,
		"config":          true,
		"CONTRIBUTORS":    false,
		"CONTRIBUTING.md": false,
		"COPYING":         false,
		"dev":             true,
		"doc":             true,
		"Dockerfile":      false,
		"internal":        true,
		"lib":             true,
		"Makefile":        false,
		"make.go":         false,
		"misc":            true,
		"old":             true,
		"pkg":             true,
		"README":          false,
		"server":          true,
		"TESTS":           false,
		"TODO":            false,
		"vendor":          true,
		"website":         true,
	}
	tarballSrc = path.Join(*flagOutDir, "camlistore.org")
)

func usage() {
	fmt.Fprintf(os.Stderr, "Usage:\n")
	flag.PrintDefaults()
	example()
	os.Exit(1)
}

func example() {
	fmt.Fprintf(os.Stderr, "Examples:\n")
	fmt.Fprintf(os.Stderr, "\tdocker run --rm --volume /tmp/camlirelease:/OUT --volume $GOPATH/src/camlistore.org/misc/docker/release/cut-source.go:/usr/local/bin/cut-source.go:ro --volume $GOPATH/src/camlistore.org:/IN:ro camlistore/go /usr/local/go/bin/go run /usr/local/bin/zip-source.go --rev WIP:/IN\n")
	fmt.Fprintf(os.Stderr, "\tdocker run --rm --volume /tmp/camlirelease:/OUT --volume $GOPATH/src/camlistore.org/misc/docker/release/zip-source.go:/usr/local/bin/cut-source.go:ro camlistore/go /usr/local/go/bin/go run /usr/local/bin/cut-source.go --rev=4e8413c5012c\n")
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
	if *flagVersion != "" {
		return fmt.Sprintf("%v (git rev %v)", *flagVersion, rev())
	}
	return rev()
}

func getCamliSrc() {
	// TODO(mpl): we could filter right within mirrorCamliSrc and
	// fetchCamliSrc so we end up directly only with what we want as source.
	// Instead, I'm doing it in two passes, which is a bit more wasteful, but
	// simpler. Maybe reconsider.
	if localCamliSource() != "" {
		mirrorCamliSrc(localCamliSource())
	} else {
		fetchCamliSrc()
	}
}

func mirrorCamliSrc(srcDir string) {
	cmd := exec.Command("cp", "-a", srcDir, tmpSource)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("Error mirroring camlistore source from %v: %v", srcDir, err)
	}
}

func fetchCamliSrc() {
	check(os.MkdirAll(tmpSource, 0777))
	check(os.Chdir(tmpSource))

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

func cpFile(dst, src string) error {
	sfi, err := os.Stat(src)
	if err != nil {
		return err
	}
	sf, err := os.Open(src)
	if err != nil {
		return err
	}
	// ok to defer because we're in main loop, and not many iterations.
	defer sf.Close()
	df, err := os.Create(dst)
	if err != nil {
		return err
	}
	n, err := io.Copy(df, sf)
	if err == nil && n != sfi.Size() {
		err = fmt.Errorf("copied wrong size for %s -> %s: copied %d; want %d", src, dst, n, sfi.Size())
	}
	cerr := df.Close()
	if err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := os.Chmod(dst, sfi.Mode()); err != nil {
		return err
	}
	if err := os.Chtimes(dst, sfi.ModTime(), sfi.ModTime()); err != nil {
		return err
	}
	return nil
}

func filter() {
	destDir := tarballSrc
	check(os.MkdirAll(destDir, 0777))

	d, err := os.Open(tmpSource)
	check(err)
	names, err := d.Readdirnames(-1)
	d.Close()
	check(err)

	found := make(map[string]struct{})
	for _, name := range names {
		isDir, ok := rootNames[name]
		if !ok {
			continue
		}
		found[name] = struct{}{}
		srcPath := path.Join(tmpSource, name)
		dstPath := path.Join(destDir, name)
		if isDir {
			cmd := exec.Command("cp", "-a", srcPath, dstPath)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				log.Fatalf("could not cp dir %v into %v: %v", name, destDir, err)
			}
			continue
		}
		check(cpFile(dstPath, srcPath))
	}
	for name, _ := range rootNames {
		if _, ok := found[name]; !ok {
			log.Fatalf("file (or directory) %v should be included in release, but not found in source", name)
		}
	}
	// we insert the version in the VERSION file, so make.go does no need git
	// in the container to detect the Camlistore version.
	check(os.Chdir(destDir))
	check(ioutil.WriteFile("VERSION", []byte(version()), 0777))
}

func checkBuild() {
	if !*flagSanity {
		return
	}
	check(os.Chdir(tarballSrc))
	check(os.Setenv("PATH", os.Getenv("PATH")+":/usr/local/go/bin/"))
	check(os.Setenv("CAMLI_GOPHERJS_GOROOT", "/usr/local/go"))
	cmd := exec.Command("go", "run", "make.go")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("could not build Camlistore from tarball contents: %v", err)
	}
	// cleanup
	check(os.RemoveAll(path.Join(tarballSrc, "tmp")))
	binDir := path.Join(tarballSrc, "bin")
	check(os.Rename(path.Join(binDir, "README"), "README.bin"))
	check(os.RemoveAll(binDir))
	check(os.MkdirAll(binDir, 0755))
	check(os.Rename("README.bin", path.Join(binDir, "README")))
}

func pack() {
	zipFile := path.Join(*flagOutDir, "camlistore-src.zip")
	check(os.Chdir(*flagOutDir))
	fw, err := os.Create(zipFile)
	check(err)
	w := zip.NewWriter(fw)

	check(filepath.Walk("camlistore.org", func(filePath string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		b, err := ioutil.ReadFile(filePath)
		if err != nil {
			return err
		}
		fh := &zip.FileHeader{
			Name:   filePath,
			Method: zip.Deflate,
		}
		fh.SetModTime(fi.ModTime())
		fh.SetMode(fi.Mode())
		f, err := w.CreateHeader(fh)
		if err != nil {
			return err
		}
		if _, err = f.Write(b); err != nil {
			return err
		}
		return nil
	}))
	check(w.Close())
	check(fw.Close())
	fmt.Printf("Camlistore source successfully packed in %v\n", zipFile)
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
	filter()
	checkBuild()
	pack()
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
