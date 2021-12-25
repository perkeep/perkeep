/*
Copyright 2013 The Perkeep Authors.

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

package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/cmdmain"
)

var (
	noBuild   = flag.Bool("nobuild", false, "do not rebuild anything")
	race      = flag.Bool("race", false, "build with race detector")
	quiet, _  = strconv.ParseBool(os.Getenv("CAMLI_QUIET"))
	wipeCache = flag.Bool("wipecache", false, "wipe the cache directory. Server cache with devcam server, client cache otherwise.")
	// Whether to build the subcommand with sqlite support. This only
	// concerns the server subcommand, which sets it to serverCmd.sqlite.
)

// The path to the Perkeep source tree. Any devcam command
// should be run from there.
var camliSrcRoot string

// sysExec is set to syscall.Exec on platforms that support it.
var sysExec func(argv0 string, argv []string, envv []string) (err error)

// runExec execs bin. If the platform doesn't support exec, it runs it and waits
// for it to finish.
func runExec(bin string, args []string, env *Env) error {
	if sysExec != nil {
		sysExec(bin, append([]string{filepath.Base(bin)}, args...), env.Flat())
	}

	cmd := exec.Command(bin, args...)
	cmd.Env = env.Flat()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("Could not run %v: %v", bin, err)
	}
	go handleSignals(cmd.Process)
	return cmd.Wait()
}

// cpDir copies the contents of src dir into dst dir.
// filter is a list of file suffixes to skip. ex: ".go"
func cpDir(src, dst string, filter []string) error {
	return filepath.Walk(src, func(fullpath string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		for _, suffix := range filter {
			if strings.HasSuffix(fi.Name(), suffix) {
				return nil
			}
		}
		suffix, err := filepath.Rel(src, fullpath)
		if err != nil {
			return fmt.Errorf("Failed to find Rel(%q, %q): %v", src, fullpath, err)
		}
		if fi.IsDir() {
			return nil
		}
		return cpFile(fullpath, filepath.Join(dst, suffix))
	})
}

func cpFile(src, dst string) error {
	sfi, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !sfi.Mode().IsRegular() {
		return fmt.Errorf("cpFile can't deal with non-regular file %s", src)
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
	return err
}

func handleSignals(camliProc *os.Process) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)
	for {
		sig := <-c
		sysSig, ok := sig.(syscall.Signal)
		if !ok {
			log.Fatal("Not a unix signal")
		}
		switch sysSig {
		case syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT:
			log.Printf("Received %v signal, terminating.", sig)
			err := camliProc.Kill()
			if err != nil {
				log.Fatalf("Failed to kill child: %v ", err)
			}
		default:
			log.Fatal("Received another signal, should not happen.")
		}
	}
}

func checkPerkeepSrcRoot() {
	args := flag.Args()
	// TODO(mpl): we should probably get rid of that limitation someday.
	if len(args) > 0 && (args[0] == "review" ||
		args[0] == "hook" ||
		args[0] == "fixv") {
		// exception for devcam review, which does its own check.
		return
	}
	if _, err := os.Stat("make.go"); err != nil {
		if !os.IsNotExist(err) {
			log.Fatalf("Could not stat make.go: %v", err)
		}
		log.Fatal("./make.go not found; devcam needs to be run from the Perkeep source tree root.")
	}
	cwd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	camliSrcRoot = cwd
}

func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("could not get current directory: %v", err)
	}
	rootlen := 1
	if runtime.GOOS == "windows" {
		rootlen += len(filepath.VolumeName(dir))
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		if len(dir) == rootlen && dir[rootlen-1] == filepath.Separator {
			return "", fmt.Errorf(".git not found. Rerun from within the Perkeep source tree")
		}
		dir = filepath.Dir(dir)
	}
}

func selfModTime() (time.Time, error) {
	var modTime time.Time
	devcamBin, err := osutil.SelfPath()
	if err != nil {
		return modTime, err
	}
	fi, err := os.Stat(devcamBin)
	if err != nil {
		return modTime, err
	}
	return fi.ModTime(), nil
}

func checkModtime() error {
	binModtime, err := selfModTime()
	if err != nil {
		return fmt.Errorf("could not get ModTime of current devcam executable: %v", err)
	}

	devcamDir := filepath.Join(camliSrcRoot, "dev", "devcam")
	d, err := os.Open(devcamDir)
	if err != nil {
		return fmt.Errorf("could not read devcam source dir %v: %v", devcamDir, err)
	}
	defer d.Close()
	fis, err := d.Readdir(-1)
	if err != nil {
		return fmt.Errorf("could not read devcam source dir %v: %v", devcamDir, err)
	}
	for _, fi := range fis {
		if fi.ModTime().After(binModtime) {
			log.Printf("**************************************************************")
			log.Printf("WARNING: your devcam binary is outdated, you should rebuild it")
			log.Printf("**************************************************************")
			return nil
		}
	}
	return nil
}

// build builds the named perkeep targets.
// Each target may have its "perkeep.org" prefix removed.
func build(targets ...string) error {
	if v, _ := strconv.ParseBool(os.Getenv("CAMLI_FAST_DEV")); v {
		// Demo mode. See dev/demo.sh.
		return nil
	}
	var fullTargets []string
	for _, t := range targets {
		t = filepath.ToSlash(t)
		if !strings.HasPrefix(t, "perkeep.org") {
			t = pathpkg.Join("perkeep.org", t)
		}
		fullTargets = append(fullTargets, t)
	}
	targetsComma := strings.Join(fullTargets, ",")
	args := []string{
		"run", "make.go",
		"--quiet",
		"--race=" + strconv.FormatBool(*race),
		"--embed_static=false",
		"--targets=" + targetsComma,
	}
	cmd := exec.Command("go", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("error building %v: %v", targetsComma, err)
	}
	return nil
}

func main() {
	cmdmain.PostFlag = func() {
		checkPerkeepSrcRoot()
		if err := checkModtime(); err != nil {
			log.Printf("Skipping freshness check: %v", err)
		}
	}

	// TODO(mpl): usage error is not really correct for devcam.
	// See if I can reimplement it while still using cmdmain.Main().
	cmdmain.Main()
}
