/*
Copyright 2012 The Camlistore Authors.

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

// The buildbot is Camlistore's continuous builder.
// This builder program is started by the master. It then rebuilds
// Go 1, GoTip, Camlistore, and runs a battery of tests for Camlistore.
// It then sends a report to the master and terminates.
// It can also respond to progress requests from the master.
package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"camlistore.org/pkg/netutil"
	"camlistore.org/pkg/osutil"
)

const (
	interval           = 60 * time.Second // polling frequency
	warmup             = 30 * time.Second // duration before we test if devcam server has started properly
	camlistoredTimeOut = time.Minute      // how long we try to dial camlistored after warmup
	go1BaseURL         = "https://storage.googleapis.com/golang/"
	go1Tarball         = "go1.5.linux-amd64.tar.gz"
	go1URL             = go1BaseURL + go1Tarball
)

var (
	// TODO(mpl): use that one, same as in master.
	altCamliRevURL = flag.String("camlirevurl", "", "alternative URL to query about the latest camlistore revision hash (e.g camlistore.org/latesthash), to alleviate hitting too often the Camlistore git repo.")
	arch           = flag.String("arch", "", "The arch we report the master(s). Defaults to runtime.GOARCH.")
	fakeTests      = flag.Bool("faketests", false, "Run fast fake tests instead of the real ones, for faster debugging.")
	help           = flag.Bool("h", false, "show this help")
	host           = flag.String("host", "0.0.0.0:8081", "listening hostname and port")
	masterHosts    = flag.String("masterhosts", "localhost:8080", "listening hostname and port of the master bots, i.e where to send the test suite reports. Comma separated list.")
	ourOS          = flag.String("os", "", "The OS we report the master(s). Defaults to runtime.GOOS.")
	skipGo1Build   = flag.Bool("skipgo1build", true, "skip initial go1 build, for debugging and quickly going to the next steps.")
	skip           = flag.String("skip", "gotip", "Test suite to skip. Valid values are \"go1\", \"gotip\", or \"all\".")
	verbose        = flag.Bool("verbose", false, "print what's going on")
	skipTLSCheck   = flag.Bool("skiptlscheck", false, "accept any certificate presented by server when uploading results.")
	taskLifespan   = flag.Int("timeout", 600, "Lifespan (in seconds) for each task run by this builder, after which the task automatically terminates. 0 or negative means infinite.")
)

var (
	testFile       = []string{"AUTHORS", "CONTRIBUTORS"}
	cacheDir       string
	camliHeadHash  string
	camliRoot      string
	camputCacheDir string
	client         = http.DefaultClient
	dbg            *debugger
	defaultPATH    string
	go1Dir         string
	goTipDir       string
	goTipHash      string

	biSuitelk        sync.Mutex
	currentTestSuite *testSuite
	currentBiSuite   *biTestSuite

	// Process of the camlistore server, so we can kill it when
	// we get killed ourselves.
	camliProc *os.Process

	// For "If-Modified-Since" requests asking for progress.
	// Updated every time a new test task/run is added to the test suite.
	lastModified time.Time
)

var devcamBin = filepath.Join("bin", "devcam")
var (
	fetchGo1Cmd        = newTask("wget", go1URL)
	untarGo1Cmd        = newTask("tar", "xzf", go1Tarball)
	hgCloneGo1Cmd      = newTask("hg", "clone", "-u", "release", "https://code.google.com/p/go")
	hgCloneGoTipCmd    = newTask("hg", "clone", "-u", "tip", "https://code.google.com/p/go")
	hgPullCmd          = newTask("hg", "pull")
	hgUpdateCmd        = newTask("hg", "update", "-C", "default")
	hgLogCmd           = newTask("hg", "log", "-r", "tip", "--template", "{node}")
	hgConfigCmd        = newTask("hg", "--config", "extensions.purge=", "purge", "--all")
	gitCloneCmd        = newTask("git", "clone", "https://camlistore.googlesource.com/camlistore")
	gitResetCmd        = newTask("git", "reset", "--hard")
	gitCleanCmd        = newTask("git", "clean", "-Xdf")
	gitPullCmd         = newTask("git", "pull")
	gitRevCmd          = newTask("git", "rev-parse", "HEAD")
	buildGoCmd         = newTask("./make.bash")
	buildCamliCmd      = newTask("go", "run", "make.go", "-v")
	runTestsCmd        = newTask(devcamBin, "test")
	runCamliCmd        = newTask(devcamBin, "server", "--wipe", "--mysql")
	camgetCmd          = newTask(devcamBin, "get")
	camputCmd          = newTask(devcamBin, "put", "file", "--permanode", testFile[0])
	camputVivifyCmd    = newTask(devcamBin, "put", "file", "--vivify", testFile[1])
	camputFilenodesCmd = newTask(devcamBin, "put", "file", "--filenodes", "pkg")
)

func usage() {
	fmt.Fprintf(os.Stderr, "\t builderBot \n")
	flag.PrintDefaults()
	os.Exit(2)
}

type debugger struct {
	lg *log.Logger
}

func (dbg *debugger) Printf(format string, v ...interface{}) {
	if dbg != nil && *verbose {
		dbg.lg.Printf(format, v...)
	}
}

func (dbg *debugger) Println(v ...interface{}) {
	if v == nil {
		return
	}
	if dbg != nil && *verbose {
		dbg.lg.Println(v...)
	}
}

type task struct {
	Program  string
	Args     []string
	Start    time.Time
	Duration time.Duration
	Err      string
	hidden   bool
}

func newTask(program string, args ...string) *task {
	return &task{Program: program, Args: args}
}

// because sometimes we do not want to modify the tsk template
// so we make a copy of it
func newTaskFrom(tsk *task) *task {
	return newTask(tsk.Program, tsk.Args...)
}

func (t *task) String() string {
	return fmt.Sprintf("%v %v", t.Program, t.Args)
}

func (t *task) Error() string {
	return t.Err
}

func (t *task) run() (string, error) {
	var err error
	defer func() {
		t.Duration = time.Now().Sub(t.Start)
		if !t.hidden {
			biSuitelk.Lock()
			currentTestSuite.addRun(t)
			biSuitelk.Unlock()
		}
	}()
	dbg.Println(t.String())
	cmd := exec.Command(t.Program, t.Args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	t.Start = time.Now()
	setTaskErr := func() {
		var sout, serr string
		if sout = stdout.String(); sout == "" {
			sout = "(empty)"
		}
		if serr = stderr.String(); serr == "" {
			serr = "(empty)"
		}
		t.Err = fmt.Sprintf("Stdout:\n%s\n\nStderr:\n%s", sout, serr)
		if err != nil {
			t.Err = fmt.Sprintf("%v\n\n%v", err, t.Err)
		}
	}
	// TODO(mpl, wathiede): make it learn about task durations.
	errc := make(chan error)
	go func() {
		errc <- cmd.Run()
	}()
	if *taskLifespan > 0 {
		select {
		case <-time.After(time.Duration(*taskLifespan) * time.Second):
			setTaskErr()
			t.Err = fmt.Sprintf("%v\n\nTask %q took too long. Giving up after %v seconds.\n",
				t.Err, t.String(), *taskLifespan)
			if cmd.Process != nil {
				if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
					dbg.Printf("Could not terminate process for task %q: %v", t.String(), err)
				}
			}
			return "", t
		case err = <-errc:
			break
		}
	} else {
		err = <-errc
	}
	if err != nil {
		setTaskErr()
		return "", t
	}
	return stdout.String(), nil
}

type testSuite struct {
	Run       []*task
	CamliHash string
	GoHash    string
	Err       string
	Start     time.Time
	IsTip     bool
}

func (ts *testSuite) addRun(tsk *task) {
	if ts == nil {
		panic("Tried adding a run to a nil testSuite")
	}
	if ts.Start.IsZero() && len(ts.Run) == 0 {
		ts.Start = tsk.Start
	}
	if tsk.Err != "" && ts.Err == "" {
		ts.Err = tsk.Err
	}
	ts.Run = append(ts.Run, tsk)
	lastModified = time.Now()
}

type biTestSuite struct {
	Local bool
	Go1   testSuite
	GoTip testSuite
}

func main() {
	flag.Usage = usage
	flag.Parse()
	if *help {
		usage()
	}

	if *skipTLSCheck {
		tr := &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
		client = &http.Client{Transport: tr}
	}

	go handleSignals()
	http.HandleFunc("/progress", progressHandler)
	go func() {
		log.Printf("Now starting to listen on %v", *host)
		if err := http.ListenAndServe(*host, nil); err != nil {
			log.Fatalf("Could not start listening on %v: %v", *host, err)
		}
	}()
	setup()

	biSuitelk.Lock()
	currentBiSuite = &biTestSuite{}
	biSuitelk.Unlock()
	for _, isTip := range [2]bool{false, true} {
		currentTestSuite = &testSuite{
			Run:   make([]*task, 0, 1),
			IsTip: isTip,
			Start: time.Now(),
		}
		// We prepare the Go tip tree as soon as in the Go 1 run, so
		// we can set GoTipHash in the test suite.
		if !isTip {
			if err := prepGoTipTree(); err != nil {
				endOfSuite(err)
				// If we failed with that in the Go 1 run, we just restart
				// from scratch instead of trying to cope with it in the Gotip run.
				// Same for buildGoTip and prepCamliTree.
				break
			}
		}

		biSuitelk.Lock()
		currentTestSuite.GoHash = goTipHash
		biSuitelk.Unlock()
		if isTip && !*fakeTests {
			if err := buildGoTip(); err != nil {
				endOfSuite(err)
				break
			}
		}
		if err := prepCamliTree(isTip); err != nil {
			endOfSuite(err)
			break
		}

		biSuitelk.Lock()
		currentTestSuite.CamliHash = camliHeadHash
		biSuitelk.Unlock()
		restorePATH()
		goDir := go1Dir
		if isTip {
			goDir = goTipDir
		}
		switchGo(goDir)
		if doSkip(isTip) {
			endOfSuite(nil)
			continue
		}
		if *fakeTests {
			if err := fakeRun(); err != nil {
				endOfSuite(err)
				continue
			}
			endOfSuite(nil)
			if isTip {
				break
			}
			continue
		}
		if err := buildCamli(); err != nil {
			endOfSuite(err)
			continue
		}
		if err := runTests(); err != nil {
			endOfSuite(err)
			continue
		}
		if err := runCamli(); err != nil {
			endOfSuite(err)
			continue
		}
		if err := hitCamliUi(); err != nil {
			endOfSuite(err)
			continue
		}
		doVivify := false
		if err := camputOne(doVivify); err != nil {
			endOfSuite(err)
			continue
		}
		doVivify = true
		if err := camputOne(doVivify); err != nil {
			endOfSuite(err)
			continue
		}
		if err := camputMany(); err != nil {
			endOfSuite(err)
			continue
		}
		endOfSuite(nil)
	}
	sanitizeRevs()
	sendReport()
}

func doSkip(isTip bool) bool {
	return *skip == "all" || (isTip && *skip == "gotip") || (!isTip && *skip == "go1")
}

func sanitizeRevs() {
	if currentBiSuite == nil {
		return
	}
	if currentBiSuite.GoTip.Start.IsZero() {
		return
	}
	if currentBiSuite.GoTip.CamliHash == "" && currentBiSuite.Go1.CamliHash == "" {
		dbg.Printf("CamliHash not set in both Go1 and GoTip test suites")
		return
	}
	if currentBiSuite.GoTip.CamliHash == "" && currentBiSuite.Go1.CamliHash == "" {
		dbg.Printf("GoHash not set in both Go1 and GoTip test suites")
		return
	}
	if currentBiSuite.GoTip.CamliHash != "" && currentBiSuite.Go1.CamliHash != "" &&
		currentBiSuite.GoTip.CamliHash != currentBiSuite.Go1.CamliHash {
		panic("CamliHash in GoTip suite and in Go1 suite differ; should not happen.")
	}
	if currentBiSuite.GoTip.GoHash != "" && currentBiSuite.Go1.GoHash != "" &&
		currentBiSuite.GoTip.GoHash != currentBiSuite.Go1.GoHash {
		panic("GoHash in GoTip suite and in Go1 suite differ; should not happen.")
	}
	if currentBiSuite.GoTip.GoHash == "" {
		currentBiSuite.GoTip.GoHash = currentBiSuite.Go1.GoHash
	}
	if currentBiSuite.Go1.GoHash == "" {
		currentBiSuite.Go1.GoHash = currentBiSuite.GoTip.GoHash
	}
	if currentBiSuite.GoTip.CamliHash == "" {
		currentBiSuite.GoTip.CamliHash = currentBiSuite.Go1.CamliHash
	}
	if currentBiSuite.Go1.CamliHash == "" {
		currentBiSuite.Go1.CamliHash = currentBiSuite.GoTip.CamliHash
	}
}

func endOfSuite(err error) {
	biSuitelk.Lock()
	defer biSuitelk.Unlock()
	if currentTestSuite.IsTip {
		currentBiSuite.GoTip = *currentTestSuite
	} else {
		currentBiSuite.Go1 = *currentTestSuite
	}
	killCamli()
	if err != nil {
		log.Printf("%v", err)
	} else {
		dbg.Println("All good.")
	}
}

func masterHostsReader(r io.Reader) ([]string, error) {
	hosts := []string{}
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		l := scanner.Text()
		u, err := url.Parse(l)
		if err != nil {
			return nil, err
		}
		if u.Host == "" {
			return nil, fmt.Errorf("URL missing Host: %q", l)
		}
		hosts = append(hosts, u.String())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return hosts, nil
}

var masterHostsFile = filepath.Join(osutil.CamliConfigDir(), "builderbot-config")

func loadMasterHosts() error {
	r, err := os.Open(masterHostsFile)
	if err != nil {
		return err
	}
	defer r.Close()

	hosts, err := masterHostsReader(r)
	if err != nil {
		return err
	}
	if *masterHosts != "" {
		*masterHosts += ","
	}
	log.Println("Additional host(s) to send our build reports:", hosts)
	*masterHosts += strings.Join(hosts, ",")
	return nil
}

func setup() {
	var err error
	defaultPATH = os.Getenv("PATH")
	if defaultPATH == "" {
		log.Fatal("PATH not set")
	}
	log.SetPrefix("BUILDER: ")
	dbg = &debugger{log.New(os.Stderr, "BUILDER: ", log.LstdFlags)}

	err = loadMasterHosts()
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("%q missing. No additional remote master(s) will receive build report.", masterHostsFile)
		} else {
			log.Printf("Error parsing master hosts file %q: %v",
				masterHostsFile, err)
		}
	}

	// the OS we run on
	if *ourOS == "" {
		*ourOS = runtime.GOOS
		if *ourOS == "" {
			// Can this happen? I don't think so, but just in case...
			panic("runtime.GOOS was not set")
		}
	}
	// the arch we run on
	if *arch == "" {
		*arch = runtime.GOARCH
		if *arch == "" {
			panic("runtime.GOARCH was not set")
		}
	}

	// cacheDir
	cacheDir = filepath.Join(os.TempDir(), "camlibot-cache")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		log.Fatalf("Could not create cache dir %v: %v", cacheDir, err)
	}

	// get go1 and gotip source
	if err := os.Chdir(cacheDir); err != nil {
		log.Fatalf("Could not cd to %v: %v", cacheDir, err)
	}
	go1Dir, err = filepath.Abs("go1")
	if err != nil {
		log.Fatalf("Problem with Go 1 dir: %v", err)
	}
	goTipDir, err = filepath.Abs("gotip")
	if err != nil {
		log.Fatalf("Problem with Go tip dir: %v", err)
	}
	// if go1 dir exist, just reuse it
	if _, err := os.Stat(go1Dir); err != nil {
		if !os.IsNotExist(err) {
			log.Fatalf("Could not stat %v: %v", go1Dir, err)
		}
		tsk := newTaskFrom(fetchGo1Cmd)
		tsk.hidden = true
		if _, err := tsk.run(); err != nil {
			log.Fatalf("Could not git clone %v: %v", go1URL, err)
		}
		tsk = newTaskFrom(untarGo1Cmd)
		tsk.hidden = true
		if _, err := tsk.run(); err != nil {
			log.Fatalf("Could not untar %v: %v", go1Tarball, err)
		}
		if err := os.Rename("go", go1Dir); err != nil {
			log.Fatalf("Could not rename go dir into %v: %v", go1Dir, err)
		}
	}

	if !*skipGo1Build {
		// build Go1
		if err := buildGo1(); err != nil {
			log.Fatal(err)
		}
	}

	// get camlistore source
	if err := os.Chdir(cacheDir); err != nil {
		log.Fatalf("Could not cd to %v: %v", cacheDir, err)
	}
	camliRoot, err = filepath.Abs("camlistore.org")
	if err != nil {
		log.Fatal(err)
	}
	// if camlistore dir already exists, reuse it
	if _, err := os.Stat(camliRoot); err != nil {
		if !os.IsNotExist(err) {
			log.Fatalf("Could not stat %v: %v", camliRoot, err)
		}
		cloneCmd := newTask(gitCloneCmd.Program, append(gitCloneCmd.Args, camliRoot)...)
		cloneCmd.hidden = true
		if _, err := cloneCmd.run(); err != nil {
			log.Fatalf("Could not git clone into %v: %v", camliRoot, err)
		}
	}

	// recording camput cache dir, so we can clean it up fast everytime
	homeDir := os.Getenv("HOME")
	if homeDir == "" {
		log.Fatal("HOME not set")
	}
	camputCacheDir = filepath.Join(homeDir, ".cache", "camlistore")
}

func buildGo1() error {
	return errors.New("building Go1 from source not supported anymore, use -skipgo1build")
	if err := os.Chdir(filepath.Join(go1Dir, "src")); err != nil {
		log.Fatalf("Could not cd to %v: %v", go1Dir, err)
	}
	tsk := newTask(buildGoCmd.Program, buildGoCmd.Args...)
	tsk.hidden = true
	if _, err := tsk.run(); err != nil {
		return err
	}
	return nil
}

func handleSignals() {
	c := make(chan os.Signal)
	sigs := []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT}
	signal.Notify(c, sigs...)
	for {
		sig := <-c
		sysSig, ok := sig.(syscall.Signal)
		if !ok {
			log.Fatal("Not a unix signal")
		}
		switch sysSig {
		case syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT:
			log.Printf("Received %v signal, terminating.", sig)
			killCamli()
			os.Exit(0)
		default:
			panic("should not get other signals here")
		}
	}
}

var plausibleHashRx = regexp.MustCompile(`^[a-f0-9]{40}$`)

func prepGoTipTree() error {
	// Doing go tip disabled as of 20140126, because we'd need to fix the hg->git change,
	// and deal with go 1.4 bootstrapping. Not worth the trouble since we're going to
	// redo the bot soon with gomote and buildlet.
	goTipHash = "Disabled"
	return nil
	if err := os.Chdir(goTipDir); err != nil {
		return fmt.Errorf("Could not cd to %v: %v", goTipDir, err)
	}
	tasks := []*task{
		newTaskFrom(hgPullCmd),
		newTaskFrom(hgUpdateCmd),
		newTaskFrom(hgLogCmd),
		newTaskFrom(hgConfigCmd),
	}
	hash := ""
	for _, t := range tasks {
		out, err := t.run()
		if err != nil {
			return fmt.Errorf("Could not prepare the Go tip tree with %v: %v", t.String(), err)
		}
		if t.String() == hgLogCmd.String() {
			hash = strings.TrimRight(out, "\n")
		}
	}
	if !plausibleHashRx.MatchString(hash) {
		return fmt.Errorf("Go rev %q does not look like an hg hash.", hash)
	}
	goTipHash = hash
	dbg.Println("current head in go tree: " + goTipHash)
	return nil
}

func buildGoTip() error {
	// not building go tip anymore
	return nil
	srcDir := filepath.Join(goTipDir, "src")
	if err := os.Chdir(srcDir); err != nil {
		return fmt.Errorf("Could not cd to %v: %v", srcDir, err)
	}
	if _, err := newTaskFrom(buildGoCmd).run(); err != nil {
		return err
	}
	return nil
}

func prepCamliTree(isTip bool) error {
	if err := os.Chdir(camliRoot); err != nil {
		return fmt.Errorf("Could not cd to %v: %v", camliRoot, err)
	}
	rev := "HEAD"
	if isTip {
		if !plausibleHashRx.MatchString(camliHeadHash) {
			// the run with Go 1 should have taken care of setting camliHeadHash
			return errors.New("camliHeadHash hasn't been set properly in the Go 1 run")
		}
		// we reset to the rev that was noted at the previous run with Go 1
		// because we want to do both runs at the same rev
		rev = camliHeadHash
	}
	resetCmd := newTask(gitResetCmd.Program, append(gitResetCmd.Args, rev)...)
	tasks := []*task{
		resetCmd,
		newTaskFrom(gitCleanCmd),
	}
	for _, t := range tasks {
		_, err := t.run()
		if err != nil {
			return fmt.Errorf("Could not prepare the Camli tree with %v: %v\n", t.String(), err)
		}
	}
	if isTip {
		// We only need to pull and get the camli head hash when in the Go 1 run
		return nil
	}
	tasks = []*task{
		newTaskFrom(gitPullCmd),
		newTaskFrom(gitRevCmd),
	}
	hash := ""
	for _, t := range tasks {
		out, err := t.run()
		if err != nil {
			return fmt.Errorf("Could not prepare the Camli tree with %v: %v\n", t.String(), err)
		}
		hash = strings.TrimRight(out, "\n")
	}
	if !plausibleHashRx.MatchString(hash) {
		return fmt.Errorf("Camlistore rev %q does not look like a git hash.", hash)
	}
	camliHeadHash = hash
	return nil
}

func restorePATH() {
	err := os.Setenv("PATH", defaultPATH)
	if err != nil {
		log.Fatalf("Could not set PATH to %v: %v", defaultPATH, err)
	}
}

func switchGo(goDir string) {
	if runtime.GOOS == "plan9" {
		panic("plan 9 not unsupported")
	}
	if goDir == goTipDir {
		// not doing gotip anymore
		return
	}
	gobin := filepath.Join(goDir, "bin", "go")
	if _, err := os.Stat(gobin); err != nil {
		log.Fatalf("Could not stat 'go' bin at %q: %v", gobin, err)
	}
	p := filepath.Join(goDir, "bin") + string(filepath.ListSeparator) + defaultPATH
	if err := os.Setenv("PATH", p); err != nil {
		log.Fatalf("Could not set PATH to %v: %v", p, err)
	}
	if err := os.Setenv("GOROOT", goDir); err != nil {
		log.Fatalf("Could not set GOROOT to %v: %v", goDir, err)
	}
}

func cleanBuildGopaths() {
	tmpDir := filepath.Join(camliRoot, "tmp")
	if _, err := os.Stat(tmpDir); err != nil {
		if !os.IsNotExist(err) {
			log.Fatalf("Could not stat %v: %v", tmpDir, err)
		}
		// Does not exist, we only have to recreate it
		// TODO(mpl): hmm maybe it should be an error that
		// it does not exist, since it also contains the
		// closure stuff?
		if err := os.MkdirAll(tmpDir, 0755); err != nil {
			log.Fatalf("Could not mkdir %v: %v", tmpDir, err)
		}
		return
	}
	f, err := os.Open(tmpDir)
	if err != nil {
		log.Fatalf("Could not open %v: %v", tmpDir, err)
	}
	defer f.Close()
	names, err := f.Readdirnames(-1)
	if err != nil {
		log.Fatalf("Could not read %v: %v", tmpDir, err)
	}
	for _, v := range names {
		if strings.HasPrefix(v, "build-gopath") {
			if err := os.RemoveAll(filepath.Join(tmpDir, v)); err != nil {
				log.Fatalf("Could not remove %v: %v", v, err)
			}
		}
	}
}

func fakeRun() error {
	if _, err := newTask("sleep", "1").run(); err != nil {
		return err
	}
	return nil
}

func buildCamli() error {
	if err := os.Chdir(camliRoot); err != nil {
		log.Fatalf("Could not cd to %v: %v", camliRoot, err)
	}
	// Clean up Camlistore's hermetic gopaths
	cleanBuildGopaths()

	if *verbose {
		tsk := newTask("go", "version")
		out, err := tsk.run()
		tsk.hidden = true
		if err != nil {
			return fmt.Errorf("failed to run 'go version': %v", err)
		}
		out = strings.TrimRight(out, "\n")
		dbg.Printf("Building Camlistore with: %v\n", out)
	}
	if _, err := newTaskFrom(buildCamliCmd).run(); err != nil {
		return err
	}
	return nil
}

func runCamli() error {
	if err := os.Chdir(camliRoot); err != nil {
		log.Fatal(err)
	}

	t := newTaskFrom(runCamliCmd)
	dbg.Println(t.String())
	cmd := exec.Command(t.Program, t.Args...)
	var output []byte
	errc := make(chan error, 1)
	t.Start = time.Now()
	go func() {
		var err error
		output, err = cmd.CombinedOutput()
		if err != nil {
			err = fmt.Errorf("%v: %v", err, string(output))
		}
		errc <- err
	}()
	select {
	case err := <-errc:
		t.Err = fmt.Sprintf("%v terminated early:\n%v\n", t.String(), err)
		biSuitelk.Lock()
		currentTestSuite.addRun(t)
		biSuitelk.Unlock()
		log.Println(t.Err)
		return t
	case <-time.After(warmup):
		biSuitelk.Lock()
		currentTestSuite.addRun(t)
		camliProc = cmd.Process
		biSuitelk.Unlock()
		dbg.Printf("%v running OK so far\n", t.String())
	}
	return nil
}

func killCamli() {
	if camliProc == nil {
		return
	}
	dbg.Println("killing Camlistore server")
	if err := camliProc.Kill(); err != nil {
		log.Fatalf("Could not kill server with pid %v: %v", camliProc.Pid, err)
	}
	camliProc = nil
	dbg.Println("")
}

func hitCamliUi() error {
	if err := hitURL("http://localhost:3179/ui/"); err != nil {
		return fmt.Errorf("could not reach camlistored UI page (dead server?): %v", err)
	}
	return nil
}

func hitURL(uri string) (err error) {
	tsk := newTask("http.Get", uri)
	defer func() {
		if err != nil {
			tsk.Err = fmt.Sprintf("%v", err)
		}
		biSuitelk.Lock()
		currentTestSuite.addRun(tsk)
		biSuitelk.Unlock()
	}()
	dbg.Println(tsk.String())
	tsk.Start = time.Now()
	u, err := url.Parse(uri)
	if err != nil {
		return fmt.Errorf("%v: could not get host:port to dial from %v: %v\n", tsk.String(), uri, err)
	}
	hostPort := u.Host
	err = netutil.AwaitReachable(hostPort, camlistoredTimeOut)
	if err != nil {
		return fmt.Errorf("%v: camlistored unreachable at %v after %v: %v\n", tsk.String(), hostPort, camlistoredTimeOut, err)
	}
	var resp *http.Response
	resp, err = http.Get(uri)
	if err != nil {
		return fmt.Errorf("%v: %v\n", tsk.String(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("%v, got StatusCode: %d\n", tsk.String(), resp.StatusCode)
	}
	return nil
}

func camputOne(vivify bool) error {
	if err := os.Chdir(camliRoot); err != nil {
		log.Fatalf("Could not cd to %v: %v", camliRoot, err)
	}

	// clean up camput caches
	if err := os.RemoveAll(camputCacheDir); err != nil {
		log.Fatalf("Problem cleaning up camputCacheDir %v: %v", camputCacheDir, err)
	}

	// push the file to camli
	tsk := newTaskFrom(camputCmd)
	if vivify {
		tsk = newTaskFrom(camputVivifyCmd)
	}
	out, err := tsk.run()
	if err != nil {
		return err
	}
	// TODO(mpl): parsing camput output is kinda weak.
	firstSHA1 := regexp.MustCompile(`.*(sha1-[a-zA-Z0-9]+)\nsha1-[a-zA-Z0-9]+\nsha1-[a-zA-Z0-9]+\n.*`)
	if vivify {
		firstSHA1 = regexp.MustCompile(`.*(sha1-[a-zA-Z0-9]+)\n.*`)
	}
	m := firstSHA1.FindStringSubmatch(out)
	if m == nil {
		return fmt.Errorf("%v: unexpected camput output\n", tsk.String())
	}
	blobref := m[1]

	// get the file's json to find out the file's blobref
	tsk = newTask(camgetCmd.Program, append(camgetCmd.Args, blobref)...)
	out, err = tsk.run()
	if err != nil {
		return err
	}
	blobrefPattern := regexp.MustCompile(`"blobRef": "(sha1-[a-zA-Z0-9]+)",\n.*`)
	m = blobrefPattern.FindStringSubmatch(out)
	if m == nil {
		return fmt.Errorf("%v: unexpected camget output\n", tsk.String())
	}
	blobref = m[1]

	// finally, get the file back
	tsk = newTask(camgetCmd.Program, append(camgetCmd.Args, blobref)...)
	out, err = tsk.run()
	if err != nil {
		return err
	}

	// and compare it with the original
	wantFile := testFile[0]
	if vivify {
		wantFile = testFile[1]
	}
	fileContents, err := ioutil.ReadFile(wantFile)
	if err != nil {
		log.Fatalf("Could not read %v: %v", wantFile, err)
	}
	if string(fileContents) != out {
		return fmt.Errorf("%v: contents fetched with camget differ from %v contents", tsk.String(), wantFile)
	}
	return nil
}

func camputMany() error {
	err := os.Chdir(camliRoot)
	if err != nil {
		log.Fatalf("Could not cd to %v: %v", camliRoot, err)
	}

	// upload the full camli pkg tree
	if _, err := newTaskFrom(camputFilenodesCmd).run(); err != nil {
		return err
	}
	return nil
}

func runTests() error {
	if err := os.Chdir(camliRoot); err != nil {
		log.Fatal(err)
	}
	if _, err := newTaskFrom(runTestsCmd).run(); err != nil {
		return err
	}
	return nil
}

const reportPrefix = "/report"

func postToURL(u string, r io.Reader) (*http.Response, error) {
	// Default to plain HTTP.
	if !(strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")) {
		u = "http://" + u
	}
	uri, err := url.Parse(u)
	if err != nil {
		return nil, err
	}

	// If the URL explicitly specifies "/" or something else, we'll POST to
	// that, otherwise default to build-time default.
	if uri.Path == "" {
		uri.Path = reportPrefix
	}

	// Save user/pass if specified in the URL.
	user := uri.User
	// But don't send user/pass in URL to server.
	uri.User = nil

	req, err := http.NewRequest("POST", uri.String(), r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "text/javascript")
	// If user/pass set on original URL, set the auth header for the request.
	if user != nil {
		pass, ok := user.Password()
		if !ok {
			log.Println("Password not set for", user.Username(), "in", u)
		}
		req.SetBasicAuth(user.Username(), pass)
	}
	return client.Do(req)
}

func sendReport() {
	biSuitelk.Lock()
	// we make a copy so we can release the lock quickly enough
	currentBiSuiteCpy := &biTestSuite{
		Go1:   currentBiSuite.Go1,
		GoTip: currentBiSuite.GoTip,
	}
	biSuitelk.Unlock()
	masters := strings.Split(*masterHosts, ",")
	OSArch := *ourOS + "_" + *arch
	toReport := struct {
		OSArch string
		Ts     *biTestSuite
	}{
		OSArch: OSArch,
		Ts:     currentBiSuiteCpy,
	}
	for _, v := range masters {
		// TODO(mpl): ipv6 too I suppose. just make a IsLocalhost func or whatever.
		// probably can borrow something from camli code for that.
		if strings.HasPrefix(v, "localhost") || strings.HasPrefix(v, "127.0.0.1") {
			toReport.Ts.Local = true
		} else {
			toReport.Ts.Local = false
		}
		report, err := json.MarshalIndent(toReport, "", "  ")
		if err != nil {
			log.Printf("JSON serialization error: %v", err)
			return
		}
		r := bytes.NewReader(report)
		resp, err := postToURL(v, r)
		if err != nil {
			log.Printf("Could not send report: %v", err)
			continue
		}
		resp.Body.Close()
	}
}

func progressHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		log.Printf("Invalid method in progress handler: %v, want GET", r.Method)
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}
	if checkLastModified(w, r, lastModified) {
		return
	}
	biSuitelk.Lock()
	if currentBiSuite != nil {
		if currentTestSuite.IsTip {
			currentBiSuite.GoTip = *currentTestSuite
		} else {
			currentBiSuite.Go1 = *currentTestSuite
		}
	}
	sanitizeRevs()
	report, err := json.MarshalIndent(currentBiSuite, "", "  ")
	if err != nil {
		biSuitelk.Unlock()
		log.Printf("JSON serialization error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	biSuitelk.Unlock()
	_, err = io.Copy(w, bytes.NewReader(report))
	if err != nil {
		log.Printf("Could not send progress report: %v", err)
	}
}

// modtime is the modification time of the resource to be served, or IsZero().
// return value is whether this request is now complete.
func checkLastModified(w http.ResponseWriter, r *http.Request, modtime time.Time) bool {
	if modtime.IsZero() {
		return false
	}

	// The Date-Modified header truncates sub-second precision, so
	// use mtime < t+1s instead of mtime <= t to check for unmodified.
	if t, err := time.Parse(http.TimeFormat, r.Header.Get("If-Modified-Since")); err == nil && modtime.Before(t.Add(1*time.Second)) {
		h := w.Header()
		delete(h, "Content-Type")
		delete(h, "Content-Length")
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	w.Header().Set("Last-Modified", modtime.UTC().Format(http.TimeFormat))
	return false
}
