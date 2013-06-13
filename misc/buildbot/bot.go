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

package main

import (
	"bytes"
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
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
)

const (
	interval    = 60 * time.Second // polling frequency
	warmup      = 60 * time.Second // duration before we test if dev-server has started properly
	historySize = 30
)

var (
	fast      = flag.Bool("fast", false, "run dummy steps instead of the actual tasks, for debugging")
	gotipdir  = flag.String("gotip", "./go", "path to the Go tip tree")
	help      = flag.Bool("h", false, "show this help")
	host      = flag.String("host", "0.0.0.0:8080", "listening hostname and port")
	nocleanup = flag.Bool("nocleanup", false, "do not clean up the tmp gopath when failing")
	verbose   = flag.Bool("verbose", false, "print what's going on")
)

var (
	testFile        = []string{"AUTHORS", "CONTRIBUTORS"}
	camliHeadHash   string
	cachedCamliRoot string
	camliRoot       string
	camputCacheDir  string
	currentTask     task
	dbg             *debugger
	defaultDir      string
	defaultPATH     string
	// doBuildCamli0 is for when we're in the go 1 test suite, and
	// doBuildCamli1 for the go tip test suite. They are set when
	// the camli source tree has been updated and indicate that
	// 'make' should be run.
	// doBuildGo indicates the go source tree has changed, hence
	// go tip should be rebuilt, and in the go tip test suite case
	// camlistore should be cleaned (pkg and bin) and rebuilt.
	doBuildGo, doBuildCamli0, doBuildCamli1 bool
	goPath                                  string
	go1Path                                 string
	goTipPath                               string
	goTipDir                                string
	goTipHash                               string
	lastErr                                 error

	historylk        sync.Mutex
	currentTestSuite *testSuite
	history          History
)

var NameToCmd = map[string]string{
	"prepRepo1":   "hg pull",
	"prepRepo2":   "hg update -C default",
	"prepRepo3":   "hg --config extensions.purge= purge --all",
	"prepRepo4":   "hg log -r tip --template {node}",
	"prepRepo5":   "git reset --hard HEAD",
	"prepRepo6":   "git clean -Xdf",
	"prepRepo7":   "git pull",
	"prepRepo8":   "git rev-parse HEAD",
	"buildGoTip1": "./make.bash",
	"buildCamli1": "make forcefull",
	"buildCamli2": "make presubmit",
	"runCamli":    "./dev-server --wipe --mysql --offline",
	"hitCamliUi1": "http://localhost:3179/ui/",
	"camget":      "./dev-camget ",
	"camput1":     "./dev-camput file --permanode " + testFile[0],
	"camput2":     "./dev-camput file --vivify " + testFile[1],
	"camput3":     "./dev-camput file --filenodes pkg",
}

func usage() {
	fmt.Fprintf(os.Stderr, "\t buildbot \n")
	flag.PrintDefaults()
	os.Exit(2)
}

type debugger struct {
	lg *log.Logger
}

func (dbg *debugger) Printf(format string, v ...interface{}) {
	if *verbose {
		dbg.lg.Printf(format, v...)
	}
}

func (dbg *debugger) Println(v ...interface{}) {
	if v == nil {
		return
	}
	if *verbose {
		dbg.lg.Println(v...)
	}
}

func setup() {
	var err error
	defaultDir, err = os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	defaultPATH = os.Getenv("PATH")
	if defaultPATH == "" {
		log.Fatal("PATH not set")
	}
	dbg = &debugger{log.New(os.Stderr, "", log.LstdFlags)}

	// check go tip tree
	goTipDir, err = filepath.Abs(*gotipdir)
	if err != nil {
		log.Fatalf("Problem with Go tip dir: %v", err)
	}
	if _, err := os.Stat(goTipDir); err != nil {
		log.Fatalf("Problem with Go tip dir: %v", err)
	}

	// setup temp gopath(s)
	goTipPath, err = ioutil.TempDir("", "camlibot-gotippath")
	if err != nil {
		log.Fatalf("problem with tempdir: %v", err)
	}
	srcDir := filepath.Join(goTipPath, "src")
	err = os.Mkdir(srcDir, 0755)
	if err != nil {
		log.Fatalf("problem with src dir: %v", err)
	}
	go1Path, err = ioutil.TempDir("", "camlibot-go1path")
	if err != nil {
		log.Fatalf("problem with tempdir: %v", err)
	}
	srcDir = filepath.Join(go1Path, "src")
	err = os.Mkdir(srcDir, 0755)
	if err != nil {
		log.Fatalf("problem with src dir: %v", err)
	}
	goPath = go1Path
	err = os.Setenv("GOPATH", goPath)
	if err != nil {
		log.Fatalf("problem setting up GOPATH: %v", err)
	}

	// set up the camlistore tree
	camliRoot = filepath.Join(srcDir, "camlistore.org")
	cacheDir := filepath.Join(os.TempDir(), "camlibot-cache")
	cachedCamliRoot = filepath.Join(cacheDir, "camlistore.org")
	if _, err := os.Stat(cachedCamliRoot); err != nil {
		// git clone
		if _, err := os.Stat(cacheDir); err != nil {
			err = os.Mkdir(cacheDir, 0755)
			if err != nil {
				log.Fatalf("problem with cache dir: %v", err)
			}
		}
		setCurrentTask("git clone https://camlistore.org/r/p/camlistore " + camliRoot)
		_, err = runCmd(getCurrentTask().Cmd)
		if err != nil {
			log.Fatalf("problem with git clone: %v", err)
		}
	} else {
		// get cache
		err = os.Rename(cachedCamliRoot, camliRoot)
		if err != nil {
			log.Fatal(err)
		}
	}

	// recording camput cache dir, so we can clean it up fast everytime
	homeDir := os.Getenv("HOME")
	if homeDir == "" {
		log.Fatal("HOME not set")
	}
	camputCacheDir = filepath.Join(homeDir, ".cache", "camlistore")
}

func cleanTempGopath() {
	if *nocleanup {
		return
	}
	err := os.Rename(camliRoot, cachedCamliRoot)
	if err != nil {
		panic(err)
	}
	err = os.RemoveAll(go1Path)
	if err != nil {
		panic(err)
	}
	err = os.RemoveAll(goTipPath)
	if err != nil {
		panic(err)
	}
}

func switchGoPath(isTip bool) {
	if isTip {
		goPath = goTipPath
	} else {
		goPath = go1Path
	}
	newRoot := filepath.Join(goPath, "src", "camlistore.org")
	if newRoot != camliRoot {
		err := os.Rename(camliRoot, newRoot)
		if err != nil {
			panic(err)
		}
		camliRoot = newRoot
		err = os.Setenv("GOPATH", goPath)
		if err != nil {
			log.Fatalf("problem setting up GOPATH: %v", err)
		}
	}
}

func handleSignals() {
	c := make(chan os.Signal)
	sigs := []os.Signal{syscall.SIGINT, syscall.SIGTERM}
	signal.Notify(c, sigs...)
	for {
		sig := <-c
		sysSig, ok := sig.(syscall.Signal)
		if !ok {
			log.Fatal("Not a unix signal")
		}
		switch sysSig {
		case syscall.SIGINT, syscall.SIGTERM:
			log.Printf("SIGINT: cleaning up %v before terminating", goPath)
			cleanTempGopath()
			os.Exit(0)
		default:
			panic("should not get other signals here")
		}
	}
}

func prepRepo() {
	doBuildGo, doBuildCamli0, doBuildCamli1 = false, false, false
	if *fast {
		if currentTestSuite == nil {
			currentTestSuite = &testSuite{
				Run:       make([]*task, 0, 1),
				GoHash:    goTipHash,
				CamliHash: camliHeadHash,
				IsTip:     false,
			}
		}
		return
	}

	// gotip
	err := os.Chdir(goTipDir)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		err := os.Chdir(defaultDir)
		if err != nil {
			panic(err)
		}
	}()
	tasks := []string{
		NameToCmd["prepRepo1"],
		NameToCmd["prepRepo2"],
		NameToCmd["prepRepo4"],
	}
	hash := ""
	for _, v := range tasks {
		setCurrentTask(v)
		out, err := runCmd(v)
		if err != nil {
			if v == NameToCmd["prepRepo1"] {
				dbg.Printf("Go repo could not be updated: %v\n", err)
				continue
			}
			log.Fatal(err)
		}
		hash = strings.TrimRight(out, "\n")
	}
	dbg.Println("previous head in go tree: " + goTipHash)
	dbg.Println("current head in go tree: " + hash)
	if hash != "" && hash != goTipHash {
		goTipHash = hash
		doBuildGo = true
		dbg.Println("Changes in go tree detected, go tip will be rebuilt")
	}

	// camli
	err = os.Chdir(camliRoot)
	if err != nil {
		log.Fatal(err)
	}
	tasks = []string{
		NameToCmd["prepRepo5"],
		NameToCmd["prepRepo6"],
		NameToCmd["prepRepo7"],
		NameToCmd["prepRepo8"],
	}
	hash = ""
	for _, v := range tasks {
		setCurrentTask(v)
		out, err := runCmd(v)
		if err != nil {
			if v == NameToCmd["prepRepo7"] {
				dbg.Printf("Camli repo could not be updated: %v\n", err)
				continue
			}
			log.Fatal(err)
		}
		hash = strings.TrimRight(out, "\n")
	}
	dbg.Println("previous head in camli tree: " + camliHeadHash)
	dbg.Println("current head in camli tree: " + hash)
	if hash != "" && hash != camliHeadHash {
		camliHeadHash = hash
		doBuildCamli0, doBuildCamli1 = true, true
		dbg.Println("Changes in camli tree detected, camlistore will be rebuilt")
	}
}

func addToPATH(gobin string) {
	splitter := ":"
	switch runtime.GOOS {
	case "windows":
		splitter = ";"
	case "plan9":
		panic("unsupported")
	}
	p := gobin + splitter + defaultPATH
	err := os.Setenv("PATH", p)
	if err != nil {
		log.Fatalf("Could not set PATH to %v: %v", p, err)
	}
}

func restorePATH() {
	err := os.Setenv("PATH", defaultPATH)
	if err != nil {
		log.Fatalf("Could not set PATH to %v: %v", defaultPATH, err)
	}
}

type task struct {
	lk sync.Mutex
	// actual command that is run for this task. it includes the command and
	// all the arguments, space separated. shell metacharacters not supported.
	Cmd      string
	start    time.Time
	Duration time.Duration
}

func getCurrentTask() *task {
	currentTask.lk.Lock()
	defer currentTask.lk.Unlock()
	return &task{Cmd: currentTask.Cmd, start: currentTask.start}
}

func setCurrentTask(cmd string) {
	currentTask.lk.Lock()
	defer currentTask.lk.Unlock()
	currentTask.Cmd = cmd
	currentTask.start = time.Now()
}

type testSuite struct {
	Run        []*task
	CamliHash  string
	GoHash     string
	failedTask int
	Err        error
	Start      time.Time
	IsTip      bool
}

type History [][2]*testSuite

func addRun(tsk *task, tskErr error) {
	historylk.Lock()
	defer historylk.Unlock()

	duration := time.Now().Sub(tsk.start)
	tsk.Duration = duration
	if len(currentTestSuite.Run) == 0 {
		currentTestSuite.Start = tsk.start
	}
	if tskErr != nil && currentTestSuite.Err == nil {
		currentTestSuite.Err = tskErr
		currentTestSuite.failedTask = len(currentTestSuite.Run)
	}
	currentTestSuite.Run = append(currentTestSuite.Run, tsk)
}

func addTestSuite() {
	historylk.Lock()
	defer historylk.Unlock()
	if len(currentTestSuite.Run) < 1 {
		return
	}
	if !currentTestSuite.IsTip {
		// Go 1
		entry := [2]*testSuite{currentTestSuite, nil}
		history = append(history, entry)
	} else {
		// Go tip
		entry := [2]*testSuite{history[len(history)-1][0], currentTestSuite}
		if len(history) > historySize {
			history = append(history[1:historySize], entry)
		} else {
			history[len(history)-1] = entry
		}
	}
}

func runCmd(tsk string) (string, error) {
	dbg.Println(getCurrentTask().Cmd)
	fields := strings.Fields(tsk)
	var args []string
	if len(fields) > 1 {
		args = fields[1:]
	}
	cmd := exec.Command(fields[0], args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("%v: %v\n", stdout.String(), stderr.String())
	}
	return stdout.String(), nil
}

func gofast(n int) {
	for i := 0; i < n; i++ {
		tsk := &task{start: time.Now()}
		fail := rand.Intn(10)
		if fail < 1 {
			dbg.Println("random fail")
			addRun(tsk, fmt.Errorf("random fail"))
			continue
		}
		addRun(tsk, nil)
	}
}

func buildGoTip() error {
	if *fast {
		gofast(3)
		return nil
	}
	err := os.Chdir(goTipDir)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		err := os.Chdir(defaultDir)
		if err != nil {
			panic(err)
		}
	}()

	tasks := []string{
		NameToCmd["prepRepo3"],
	}
	for _, v := range tasks {
		setCurrentTask(v)
		tsk := getCurrentTask()
		_, err := runCmd(tsk.Cmd)
		addRun(tsk, err)
		if err != nil {
			return err
		}
	}
	err = os.Chdir(filepath.Join(goTipDir, "src"))
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		err := os.Chdir(defaultDir)
		if err != nil {
			panic(err)
		}
	}()

	setCurrentTask(NameToCmd["buildGoTip1"])
	tsk := getCurrentTask()
	_, err = runCmd(tsk.Cmd)
	addRun(tsk, err)
	if err != nil {
		return err
	}
	return nil
}

func buildCamli(isTip bool) error {
	if *fast {
		gofast(5)
		return nil
	}
	switchGoPath(isTip)
	err := os.Chdir(camliRoot)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		err := os.Chdir(defaultDir)
		if err != nil {
			panic(err)
		}
	}()

	if doBuildGo && isTip {
		if *verbose {
			dbg.Println("cleaning up gopath/pkg and gopath/bin for full rebuild")
		}
		// erase and rebuild everything
		pkgdir := filepath.Join("..", "..", "pkg")
		err := os.RemoveAll(pkgdir)
		if err != nil {
			log.Fatalf("failed to remove %v: %v", pkgdir, err)
		}
		bindir := filepath.Join("..", "..", "bin")
		err = os.RemoveAll(bindir)
		if err != nil {
			log.Fatalf("failed to remove %v: %v", bindir, err)
		}
	}

	if *verbose {
		setCurrentTask("go version")
		out, err := runCmd(getCurrentTask().Cmd)
		if err != nil {
			log.Fatalf("failed to run 'go version': %v", err)
		}
		out = strings.TrimRight(out, "\n")
		dbg.Printf("Building camlistore in %v with: %v\n", goPath, out)
	}

	tasks := []string{}
	if doBuildCamli0 || doBuildCamli1 || (doBuildGo && isTip) {
		tasks = append(tasks, NameToCmd["buildCamli1"])
	}
	tasks = append(tasks, NameToCmd["buildCamli2"])
	for _, v := range tasks {
		setCurrentTask(v)
		tsk := getCurrentTask()
		_, err := runCmd(tsk.Cmd)
		addRun(tsk, err)
		if err != nil {
			return err
		}
	}
	if doBuildGo && isTip {
		doBuildGo = false
		doBuildCamli1 = false
	}
	if isTip {
		doBuildCamli1 = false
	} else {
		doBuildCamli0 = false
	}

	return nil
}

func runCamli() (*os.Process, error) {
	if *fast {
		gofast(1)
		return nil, nil
	}
	err := os.Chdir(camliRoot)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		err := os.Chdir(defaultDir)
		if err != nil {
			panic(err)
		}
	}()

	setCurrentTask(NameToCmd["runCamli"])
	dbg.Println(getCurrentTask().Cmd)
	fields := strings.Fields(getCurrentTask().Cmd)
	args := fields[1:]
	cmd := exec.Command(fields[0], args...)

	var output []byte
	errc := make(chan error, 1)
	go func() {
		output, err = cmd.CombinedOutput()
		errc <- err
	}()
	select {
	case err := <-errc:
		dbg.Printf("dev server DEAD:\n%s\n", output)
		tsk := getCurrentTask()
		addRun(tsk, err)
		return nil, fmt.Errorf("%v: server failed to start\n", tsk.Cmd)
	case <-time.After(warmup):
		dbg.Println("dev server OK")
		addRun(getCurrentTask(), nil)
	}
	return cmd.Process, nil
}

func killCamli(proc *os.Process) {
	if *fast {
		return
	}
	dbg.Println("killing dev server")
	err := proc.Kill()
	if err != nil {
		log.Fatalf("Could not kill dev-server: %v", err)
	}
	dbg.Println("")
}

func hitURL(url string) error {
	setCurrentTask(fmt.Sprintf("http.Get(\"%s\")", url))
	dbg.Println(getCurrentTask().Cmd)
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("%v: %v\n", getCurrentTask().Cmd, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("%v, got StatusCode: %d\n", getCurrentTask().Cmd, resp.StatusCode)
	}
	return nil
}

func hitCamliUi() error {
	if *fast {
		gofast(2)
		return nil
	}
	urls := []string{
		NameToCmd["hitCamliUi1"],
	}
	var err error
	for _, v := range urls {
		lerr := hitURL(v)
		addRun(getCurrentTask(), lerr)
		if lerr != nil {
			err = lerr
		}
	}
	return err
}

func camputOne(vivify bool) error {
	if *fast {
		gofast(3)
		return nil
	}
	err := os.Chdir(camliRoot)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		err := os.Chdir(defaultDir)
		if err != nil {
			panic(err)
		}
	}()
	// clean up camput caches
	err = os.RemoveAll(camputCacheDir)
	if err != nil {
		log.Fatalf("Problem cleaning up camputCacheDir %v: %v", camputCacheDir, err)
	}

	// push the file to camli
	tskString := NameToCmd["camput1"]
	if vivify {
		tskString = NameToCmd["camput2"]
	}
	setCurrentTask(tskString)
	tsk := getCurrentTask()
	out, err := runCmd(tsk.Cmd)
	addRun(tsk, err)
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
		return fmt.Errorf("%v: unexpected camput output\n", getCurrentTask().Cmd)
	}
	blobref := m[1]

	// get the file's json to find out the file's blobref
	setCurrentTask(NameToCmd["camget"] + blobref)
	tsk = getCurrentTask()
	out, err = runCmd(tsk.Cmd)
	addRun(tsk, err)
	if err != nil {
		return err
	}
	blobrefPattern := regexp.MustCompile(`"blobRef": "(sha1-[a-zA-Z0-9]+)",\n.*`)
	m = blobrefPattern.FindStringSubmatch(out)
	if m == nil {
		return fmt.Errorf("%v: unexpected camget output\n", getCurrentTask().Cmd)
	}
	blobref = m[1]

	// finally, get the file back
	setCurrentTask(NameToCmd["camget"] + blobref)
	tsk = getCurrentTask()
	out, err = runCmd(tsk.Cmd)
	addRun(tsk, err)
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
		return fmt.Errorf("%v: contents fetched with camget differ from %v contents", getCurrentTask().Cmd, wantFile)
	}
	return nil
}

func camputMany() error {
	if *fast {
		gofast(1)
		return nil
	}
	err := os.Chdir(camliRoot)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		err := os.Chdir(defaultDir)
		if err != nil {
			panic(err)
		}
	}()
	// upload the full camli pkg tree
	setCurrentTask(NameToCmd["camput3"])
	tsk := getCurrentTask()
	_, err = runCmd(tsk.Cmd)
	addRun(tsk, err)
	if err != nil {
		return err
	}
	return nil
}

func handleErr(err error, proc *os.Process) {
	lastErr = err
	dbg.Printf("%v", err)
	if proc != nil {
		killCamli(proc)
	}
	addTestSuite()
}

func main() {
	flag.Usage = usage
	flag.Parse()
	if *help {
		usage()
	}

	setup()
	defer cleanTempGopath()
	go handleSignals()

	http.HandleFunc(okPrefix, okHandler)
	http.HandleFunc(failPrefix, failHandler)
	http.HandleFunc(currentPrefix, progressHandler)
	http.HandleFunc("/", statusHandler)
	go http.ListenAndServe(*host, nil)

	tryCount := 0
	for {
		if lastErr == nil || tryCount > 1 {
			tryCount = 0
			lastErr = nil
			prepRepo()
		}
		if doBuildGo || doBuildCamli0 || doBuildCamli1 || lastErr != nil {
			for _, isTip := range [2]bool{false, true} {
				restorePATH()
				currentTestSuite = &testSuite{
					Run:       make([]*task, 0, 1),
					GoHash:    goTipHash,
					CamliHash: camliHeadHash,
					IsTip:     isTip,
				}
				if isTip {
					addToPATH(filepath.Join(goTipDir, "bin"))
					if doBuildGo {
						err := buildGoTip()
						if err != nil {
							handleErr(err, nil)
							continue
						}
					}
				}

				err := buildCamli(isTip)
				if err != nil {
					handleErr(err, nil)
					continue
				}
				proc, err := runCamli()
				if err != nil {
					handleErr(err, nil)
					continue
				}
				err = hitCamliUi()
				if err != nil {
					handleErr(err, proc)
					continue
				}
				doVivify := false
				err = camputOne(doVivify)
				if err != nil {
					handleErr(err, proc)
					continue
				}
				doVivify = true
				err = camputOne(doVivify)
				if err != nil {
					handleErr(err, proc)
					continue
				}
				err = camputMany()
				if err != nil {
					handleErr(err, proc)
					continue
				}

				dbg.Println("All good.")
				killCamli(proc)
				addTestSuite()
			}
			tryCount++
		}
		setCurrentTask(fmt.Sprintf("time.Sleep(%d)", interval))
		dbg.Println(getCurrentTask().Cmd)
		time.Sleep(interval)
	}
}

var (
	okPrefix      = "/ok/"
	failPrefix    = "/fail/"
	currentPrefix = "/current"
	statusTpl     = template.Must(template.New("status").Funcs(tmplFuncs).Parse(reportHTML))
	taskTpl       = template.Must(template.New("task").Parse(taskHTML))
	testSuiteTpl  = template.Must(template.New("ok").Parse(testSuiteHTML))
)

var tmplFuncs = template.FuncMap{
	"camliRepoURL": camliRepoURL,
	"goRepoURL":    goRepoURL,
	"shortHash":    shortHash,
}

// unlocked; history needs to be protected from the caller.
func getPastTestSuite(key string) (*testSuite, error) {
	idx := 0
	date := ""
	if strings.HasPrefix(key, "gotip-") {
		date = strings.Replace(key, "gotip-", "", -1)
		idx = 1
	} else {
		date = strings.Replace(key, "go1-", "", -1)
	}
	for _, v := range history {
		if v[idx].Start.String() == date {
			return v[idx], nil
		}
	}
	return nil, fmt.Errorf("%v not found in history", date)
}

type progressData struct {
	Ts      *testSuite
	Current string
}

func okHandler(w http.ResponseWriter, r *http.Request) {
	t := strings.Replace(r.URL.Path, okPrefix, "", -1)
	historylk.Lock()
	defer historylk.Unlock()
	ts, err := getPastTestSuite(t)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	dat := &progressData{
		Ts: ts,
	}
	err = testSuiteTpl.Execute(w, dat)
	if err != nil {
		log.Printf("ok template: %v\n", err)
	}
}

func progressHandler(w http.ResponseWriter, r *http.Request) {
	historylk.Lock()
	defer historylk.Unlock()
	dat := &progressData{
		Ts:      currentTestSuite,
		Current: getCurrentTask().Cmd,
	}
	err := testSuiteTpl.Execute(w, dat)
	if err != nil {
		log.Printf("progress template: %v\n", err)
	}
}

type TaskReport struct {
	Cmd string
	Err error
}

func failHandler(w http.ResponseWriter, r *http.Request) {
	t := strings.Replace(r.URL.Path, failPrefix, "", -1)
	historylk.Lock()
	defer historylk.Unlock()
	ts, err := getPastTestSuite(t)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	taskReport := &TaskReport{Cmd: ts.Run[ts.failedTask].Cmd, Err: ts.Err}
	err = taskTpl.Execute(w, taskReport)
	if err != nil {
		log.Printf("fail template: %v\n", err)
	}
}

type status struct {
	Hs History
	Ts *testSuite
}

func invertedHistory() (inverted History) {
	historylk.Lock()
	defer historylk.Unlock()
	inverted = make(History, len(history))
	endpos := len(history) - 1
	for k, v := range history {
		inverted[endpos-k] = v
	}
	return inverted
}

func statusHandler(w http.ResponseWriter, r *http.Request) {
	stat := &status{
		Hs: invertedHistory(),
		Ts: currentTestSuite,
	}
	err := statusTpl.Execute(w, stat)
	if err != nil {
		log.Printf("status template: %v\n", err)
	}
}

// shortHash returns a short version of a hash.
func shortHash(hash string) string {
	if len(hash) > 12 {
		hash = hash[:12]
	}
	return hash
}

func goRepoURL(hash string) string {
	return "https://code.google.com/p/go/source/detail?r=" + hash
}

func camliRepoURL(hash string) string {
	return "http://camlistore.org/code/?p=camlistore.git;a=commit;h=" + hash
}

// style inspired from $GOROOT/misc/dashboard/app/build/ui.html
var styleHTML = `
<style>
	body {
		font-family: sans-serif;
		padding: 0; margin: 0;
	}
	h1, h2 {
		margin: 0;
		padding: 5px;
	}
	h1 {
		background: #eee;
	}
	h2 {
		margin-top: 20px;
	}
	.build, .packages {
		margin: 5px;
		border-collapse: collapse;
	}
	.build td, .build th, .packages td, .packages th {
		vertical-align: top;
		padding: 2px 4px;
		font-size: 10pt;
	}
	.build tr.commit:nth-child(2n) {
		background-color: #f0f0f0;
	}
	.build .hash {
		font-family: monospace;
		font-size: 9pt;
	}
	.build .result {
		text-align: center;
		width: 2em;
	}
	.col-hash, .col-result {
		border-right: solid 1px #ccc;
	}
	.build .arch {
		font-size: 66%;
		font-weight: normal;
	}
	.build .time {
		color: #666;
	}
	.build .ok {
		font-size: 83%;
	}
	a.ok {
		text-decoration:none;
	}
	.build .desc, .build .time, .build .user {
		white-space: nowrap;
	}
	.paginate {
		padding: 0.5em;
	}
	.paginate a {
		padding: 0.5em;
		background: #eee;
		color: blue;
	}
	.paginate a.inactive {
		color: #999;
	}
	.fail {
		color: #C00;
	}
</style>
`

var reportHTML = `
<!DOCTYPE HTML>
<html>
	<head>
		<title>Camlistore tests Dashboard</title>` +
	styleHTML + `
	</head>
	<body>

	<h1>Camlibot status</h1>

	<table class="build">
	<colgroup class="col-hash" span="1"></colgroup>
	<colgroup class="build" span="1"></colgroup>
	<colgroup class="build" span="1"></colgroup>
	<colgroup class="user" span="1"></colgroup>
	<colgroup class="user" span="1"></colgroup>
	<tr>
	<!-- extra row to make alternating colors use dark for first result -->
	</tr>
	<tr>
	<th>&nbsp;</th>
	<th colspan="1">Go tip hash</th>
	<th colspan="1">Camli HEAD hash</th>
	<th colspan="1">Go1</th>
	<th colspan="1">Gotip</th>
	</tr>
	{{if .Ts}}
		{{if .Ts.IsTip | not}}
			<tr class="commit">
				<td class="hash">{{.Ts.Start}}</td>
				<td class="hash">
					<a href="{{goRepoURL .Ts.GoHash}}">{{shortHash .Ts.GoHash}}</a>
				</td>
				<td class="hash">
					<a href="{{camliRepoURL .Ts.CamliHash}}">{{shortHash .Ts.CamliHash}}</a>
				</td>
				<td class="result">
					<a href="` + currentPrefix + `" class="ok">In progress</a>
				</td>
			</tr>
		{{end}}
	{{end}}
	{{if .Hs}}
		{{range $tss := .Hs}}
			<tr class="commit">
			{{range $k, $ts := $tss}}
			{{if $k | not}}
				<td class="hash">{{$ts.Start}}</td>
				<td class="hash">
					<a href="{{goRepoURL $ts.GoHash}}">{{shortHash $ts.GoHash}}</a>
				</td>
				<td class="hash">
					<a href="{{camliRepoURL $ts.CamliHash}}">{{shortHash $ts.CamliHash}}</a>
				</td>
				<td class="result">
				{{if $ts.Err}}
					<a href="` + failPrefix + `go1-{{$ts.Start}}" class="fail">fail</a>
				{{else}}
					<a href="` + okPrefix + `go1-{{$ts.Start}}" class="ok">ok</a>
				{{end}}
				</td>
			{{else}}
				<td class="result">
				{{if $ts}}
					{{if $ts.Err}}
						<a href="` + failPrefix + `gotip-{{$ts.Start}}" class="fail">fail</a>
					{{else}}
						<a href="` + okPrefix + `gotip-{{$ts.Start}}" class="ok">ok</a>
					{{end}}
				{{else}}
					<a href="` + currentPrefix + `" class="ok">In progress</a>
				{{end}}
				</td>
			{{end}}
			{{end}}
			</tr>
		{{end}}
	{{end}}
	</table>

	</body>
</html>
`

var testSuiteHTML = `
<!DOCTYPE HTML>
<html>
	<head>
		<title>Camlistore tests Dashboard</title>` +
	styleHTML + `
	</head>
	<body>
	{{if .Ts}}
		<h2> Testsuite for {{if .Ts.IsTip}}Go tip{{else}}Go 1{{end}} at {{.Ts.Start}} </h2>
		<table class="build">
		<colgroup class="col-result" span="1"></colgroup>
		<colgroup class="col-result" span="1"></colgroup>
		<tr>
			<!-- extra row to make alternating colors use dark for first result -->
		</tr>
		<tr>
			<th colspan="1">Step</th>
			<th colspan="1">Duration</th>
		</tr>
		{{range $k, $v := .Ts.Run}}
		<tr>
			<td>{{$v.Cmd}}</td>
			<td>{{$v.Duration}}</td>
		</tr>
		{{end}}
		{{if .Current}}
		<tr>
			<td>{{.Current}}</td>
			<td>(running...)</td>
		</tr>
		{{end}}
		</table>
	{{end}}
	</body>
</html>
`

var taskHTML = `
<!DOCTYPE HTML>
<html>
	<head>
		<title>Camlistore tests Dashboard</title>
	</head>
	<body>
{{if .Cmd}}
	<h2>Command:</h2>
	<p>{{.Cmd}}</p>
{{end}}
{{if .Err}}
	<h2>Error:</h2>
	<pre>
	{{.Err}}
	</pre>
{{end}}
	</body>
</html>
`
