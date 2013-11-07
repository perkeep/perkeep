/*
Copyright 2013 The Camlistore Authors.

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
// This master program monitors changes to the Go and Camlistore trees,
// then rebuilds and restarts a builder when a change dictates as much.
// It receives a report from a builder when it has finished running
// a test suite, but it can also poll a builder before completion
// to get a progress report.
// It also serves the web requests.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"net"
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
	interval      = 60 * time.Second // polling frequency
	historySize   = 30
	maxStderrSize = 1 << 20 // Keep last 1 MB of logging.
)

var (
	altCamliRevURL = flag.String("camlirevurl", "", "alternative URL to query about the latest camlistore revision hash (e.g camlistore.org/latesthash), to alleviate hitting too often the Camlistore git repo.")
	builderOpts    = flag.String("builderopts", "", "list of comma separated options that will be passed to the builders (ex: '-verbose=true,-faketests=true,-skipgo1build=true'). Mainly for debugging.")
	builderPort    = flag.String("builderport", "8081", "listening port for the builder bot")
	builderSrc     = flag.String("buildersrc", "", "Go source file for the builder bot. For testing changes to the builder bot that haven't been committed yet.")
	getGo          = flag.Bool("getgo", false, "Do not use the system's Go to build the builder, use the downloaded gotip instead.")
	help           = flag.Bool("h", false, "show this help")
	host           = flag.String("host", "0.0.0.0:8080", "listening hostname and port")
	peers          = flag.String("peers", "", "comma separated list of host:port masters (besides this one) our builders will report to.")
	verbose        = flag.Bool("verbose", false, "print what's going on")
)

var (
	camliHeadHash           string
	camliRoot               string
	dbg                     *debugger
	defaultDir              string
	doBuildGo, doBuildCamli bool
	goTipDir                string
	goTipHash               string

	historylk sync.Mutex
	history   = make(map[string][]*biTestSuite) // key is the OS_Arch on which the tests were run

	inProgresslk sync.Mutex
	inProgress   *testSuite
	// Process of the local builder bot, so we can kill it
	// when we get killed.
	builderProc *os.Process

	// For "If-Modified-Since" requests on the status page.
	// Updated every time a new test suite starts or ends.
	lastModified time.Time

	// Override the os.Stderr used by the default logger so we can provide
	// more debug info on status page.
	logStderr   = newLockedBuffer()
	multiWriter io.Writer
)

// lockedBuffer protects all Write calls with a mutex.  Users of lockedBuffer
// must wrap any calls to Bytes, and use of the resulting slice with calls to
// Lock/Unlock.
type lockedBuffer struct {
	sync.Mutex // guards ringBuffer
	*ringBuffer
}

func newLockedBuffer() *lockedBuffer {
	return &lockedBuffer{ringBuffer: newRingBuffer(maxStderrSize)}
}

func (lb *lockedBuffer) Write(b []byte) (int, error) {
	lb.Lock()
	defer lb.Unlock()
	return lb.ringBuffer.Write(b)
}

type ringBuffer struct {
	buf []byte
	off int // End of ring buffer.
	l   int // Length of ring buffer filled.
}

func newRingBuffer(maxSize int) *ringBuffer {
	return &ringBuffer{
		buf: make([]byte, maxSize),
	}
}

func (rb *ringBuffer) Bytes() []byte {
	if (rb.off - rb.l) >= 0 {
		// Partially full buffer with no wrap.
		return rb.buf[rb.off-rb.l : rb.off]
	}

	// Buffer has been wrapped, copy second half then first half.
	start := rb.off - rb.l
	if start < 0 {
		start = rb.off
	}
	b := make([]byte, 0, cap(rb.buf))
	b = append(b, rb.buf[start:]...)
	b = append(b, rb.buf[:start]...)
	return b
}

func (rb *ringBuffer) Write(buf []byte) (int, error) {
	ringLen := cap(rb.buf)
	for i, b := range buf {
		rb.buf[(rb.off+i)%ringLen] = b
	}
	rb.off = (rb.off + len(buf)) % ringLen
	rb.l = rb.l + len(buf)
	if rb.l > ringLen {
		rb.l = ringLen
	}
	return len(buf), nil
}

var devcamBin = filepath.Join("bin", "devcam")
var (
	hgCloneGoTipCmd = newTask("hg", "clone", "-u", "tip", "https://code.google.com/p/go")
	hgPullCmd       = newTask("hg", "pull")
	hgLogCmd        = newTask("hg", "log", "-r", "tip", "--template", "{node}")
	gitCloneCmd     = newTask("git", "clone", "https://camlistore.googlesource.com/camlistore")
	gitPullCmd      = newTask("git", "pull")
	gitRevCmd       = newTask("git", "rev-parse", "HEAD")
	buildGoCmd      = newTask("./make.bash")
)

func usage() {
	fmt.Fprintf(os.Stderr, "\t masterBot \n")
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
}

func newTask(program string, args ...string) *task {
	return &task{Program: program, Args: args}
}

func (t *task) String() string {
	return fmt.Sprintf("%v %v", t.Program, t.Args)
}

func (t *task) run() (string, error) {
	var err error
	dbg.Println(t.String())
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(t.Program, t.Args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err = cmd.Run(); err != nil {
		return "", fmt.Errorf("%v: %v", err, stderr.String())
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

type biTestSuite struct {
	Local bool
	Go1   *testSuite
	GoTip *testSuite
}

func addTestSuites(OSArch string, ts *biTestSuite) {
	if ts == nil {
		return
	}
	historylk.Lock()
	if ts.Local {
		inProgresslk.Lock()
		defer inProgresslk.Unlock()
	}
	defer historylk.Unlock()
	historyOSArch := history[OSArch]
	if len(historyOSArch) > historySize {
		historyOSArch = append(historyOSArch[1:historySize], ts)
	} else {
		historyOSArch = append(historyOSArch, ts)
	}
	history[OSArch] = historyOSArch
	if ts.Local {
		inProgress = nil
	}
	lastModified = time.Now()
}

func main() {
	flag.Usage = usage
	flag.Parse()
	if *help {
		usage()
	}

	go handleSignals()
	http.HandleFunc(okPrefix, okHandler)
	http.HandleFunc(failPrefix, failHandler)
	http.HandleFunc(progressPrefix, progressHandler)
	http.HandleFunc(stderrPrefix, logHandler)
	http.HandleFunc("/", statusHandler)
	http.HandleFunc(reportPrefix, reportHandler)
	go func() {
		log.Printf("Now starting to listen on %v", *host)
		if err := http.ListenAndServe(*host, nil); err != nil {
			log.Fatalf("Could not start listening on %v: %v", *host, err)
		}
	}()
	setup()

	for {
		goHash, err := pollGoChange()
		if err != nil {
			log.Fatal(err)
		}
		camliHash, err := pollCamliChange()
		if err != nil {
			log.Fatal(err)
		}
		if doBuildGo || doBuildCamli {
			if err := buildBuilder(); err != nil {
				log.Printf("Could not build builder bot: %v", err)
				goto Sleep
			}
			cmd, err := startBuilder(goHash, camliHash)
			if err != nil {
				log.Printf("Could not start builder bot: %v", err)
				goto Sleep
			}
			dbg.Println("Waiting for builder to finish")
			if err := cmd.Wait(); err != nil {
				log.Printf("builder finished with error: %v", err)
			}
			resetBuilderState()
		}
	Sleep:
		tsk := newTask("time.Sleep", interval.String())
		dbg.Println(tsk.String())
		time.Sleep(interval)
	}
}

func resetBuilderState() {
	inProgresslk.Lock()
	defer inProgresslk.Unlock()
	builderProc = nil
	inProgress = nil
}

func setup() {
	// Install custom stderr for display in status webpage.
	multiWriter = io.MultiWriter(logStderr, os.Stderr)
	log.SetOutput(multiWriter)

	var err error
	defaultDir, err = os.Getwd()
	if err != nil {
		log.Fatalf("Could not get current dir: %v", err)
	}
	dbg = &debugger{log.New(multiWriter, "", log.LstdFlags)}

	goTipDir, err = filepath.Abs("gotip")
	if err != nil {
		log.Fatal(err)
	}
	// if gotip dir exist, just reuse it
	if _, err := os.Stat(goTipDir); err != nil {
		if !os.IsNotExist(err) {
			log.Fatalf("Could not stat %v: %v", goTipDir, err)
		}
		if _, err := hgCloneGoTipCmd.run(); err != nil {
			log.Fatalf("Could not hg clone %v: %v", goTipDir, err)
		}
		if err := os.Rename("go", goTipDir); err != nil {
			log.Fatalf("Could not rename go dir into %v: %v", goTipDir, err)
		}
	}

	if _, err := exec.LookPath("go"); err != nil {
		// Go was not found on this machine, but we've already
		// downloaded gotip anyway, so let's install it and
		// use it to build the builder bot.
		*getGo = true
	}

	if *getGo {
		// set PATH
		splitter := ":"
		switch runtime.GOOS {
		case "windows":
			splitter = ";"
		case "plan9":
			panic("unsupported OS")
		}
		p := os.Getenv("PATH")
		if p == "" {
			log.Fatal("PATH not set")
		}
		p = filepath.Join(goTipDir, "bin") + splitter + p
		if err := os.Setenv("PATH", p); err != nil {
			log.Fatalf("Could not set PATH to %v: %v", p, err)
		}
		// and check if we already have a gotip binary
		if _, err := exec.LookPath("go"); err != nil {
			// if not, build gotip
			if err := buildGo(); err != nil {
				log.Fatal(err)
			}
		}
	}

	// get camlistore source
	if err := os.Chdir(defaultDir); err != nil {
		log.Fatalf("Could not cd to %v: %v", defaultDir, err)
	}
	camliRoot, err = filepath.Abs("src/camlistore.org")
	if err != nil {
		log.Fatal(err)
	}
	// if camlistore dir already exists, reuse it
	if _, err := os.Stat(camliRoot); err != nil {
		if !os.IsNotExist(err) {
			log.Fatalf("Could not stat %v: %v", camliRoot, err)
		}
		cloneCmd := newTask(gitCloneCmd.Program, append(gitCloneCmd.Args, camliRoot)...)
		if _, err := cloneCmd.run(); err != nil {
			log.Fatalf("Could not git clone into %v: %v", camliRoot, err)
		}
	}
	// override GOPATH to only point to our freshly updated camlistore source.
	if err := os.Setenv("GOPATH", defaultDir); err != nil {
		log.Fatalf("Could not set GOPATH to %v: %v", defaultDir, err)
	}
}

func buildGo() error {
	if err := os.Chdir(filepath.Join(goTipDir, "src")); err != nil {
		log.Fatalf("Could not cd to %v: %v", goTipDir, err)
	}
	if _, err := buildGoCmd.run(); err != nil {
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
			log.Printf("Received %v signal; cleaning up and terminating.", sig)
			if builderProc != nil {
				if err := builderProc.Kill(); err != nil {
					log.Fatalf("Failed to kill our builder bot with pid %v: %v.", builderProc.Pid, err)
				}
			}
			os.Exit(0)
		default:
			panic("should not get other signals here")
		}
	}
}

func pollGoChange() (string, error) {
	doBuildGo = false
	if err := os.Chdir(goTipDir); err != nil {
		log.Fatalf("Could not cd to %v: %v", goTipDir, err)
	}
	tasks := []*task{
		hgPullCmd,
		hgLogCmd,
	}
	hash := ""
	for _, t := range tasks {
		out, err := t.run()
		if err != nil {
			if t.String() == hgPullCmd.String() {
				log.Printf("Could not pull the Go tree with %v: %v", t.String(), err)
				continue
			}
			log.Printf("Could not prepare the Go tree with %v: %v", t.String(), err)
			return "", err
		}
		hash = strings.TrimRight(out, "\n")
	}
	dbg.Println("previous head in go tree: " + goTipHash)
	dbg.Println("current head in go tree: " + hash)
	if hash != "" && hash != goTipHash {
		goTipHash = hash
		doBuildGo = true
		dbg.Println("Changes in go tree detected; a builder will be started.")
	}
	return hash, nil
}

var plausibleHashRx = regexp.MustCompile(`^[a-f0-9]{40}$`)

func altCamliPolling() (string, error) {
	resp, err := http.Get(*altCamliRevURL)
	if err != nil {
		return "", fmt.Errorf("Could not get camliHash from %v: %v", *altCamliRevURL, err)
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("Could not read camliHash from %v's response: %v", *altCamliRevURL, err)
	}
	hash := strings.TrimSpace(string(body))
	if !plausibleHashRx.MatchString(hash) {
		return "", fmt.Errorf("%v's response does not look like a git hash.", *altCamliRevURL)
	}
	return hash, nil
}

func pollCamliChange() (string, error) {
	doBuildCamli = false
	altDone := false
	var err error
	rev := ""
	if *altCamliRevURL != "" {
		rev, err = altCamliPolling()
		if err != nil {
			log.Print(err)
			dbg.Println("Defaulting to the camli repo instead")
		} else {
			dbg.Printf("Got camli rev %v from %v\n", rev, *altCamliRevURL)
			altDone = true
		}
	}
	if !altDone {
		if err := os.Chdir(camliRoot); err != nil {
			log.Fatalf("Could not cd to %v: %v", camliRoot, err)
		}
		tasks := []*task{
			gitPullCmd,
			gitRevCmd,
		}
		for _, t := range tasks {
			out, err := t.run()
			if err != nil {
				if t.String() == gitPullCmd.String() {
					log.Printf("Could not pull the Camli repo with %v: %v\n", t.String(), err)
					continue
				}
				log.Printf("Could not prepare the Camli tree with %v: %v\n", t.String(), err)
				return "", err
			}
			rev = strings.TrimRight(out, "\n")
		}
	}
	dbg.Println("previous head in camli tree: " + camliHeadHash)
	dbg.Println("current head in camli tree: " + rev)
	if rev != "" && rev != camliHeadHash {
		camliHeadHash = rev
		doBuildCamli = true
		dbg.Println("Changes in camli tree detected; a builder will be started.")
	}
	return rev, nil
}

const builderBotBin = "builderBot"

func buildBuilder() error {
	// TODO(Bill, mpl): import common auth module for both the master and builder. Or the multi-files
	// approach. Whatever's cleaner.
	source := *builderSrc
	if source == "" {
		if *altCamliRevURL != "" {
			// since we used altCamliRevURL (and not git pull), our camli tree
			// and hence our buildbot source code, might not be up to date.
			if err := os.Chdir(camliRoot); err != nil {
				log.Fatalf("Could not cd to %v: %v", camliRoot, err)
			}
			out, err := gitRevCmd.run()
			if err != nil {
				return fmt.Errorf("Could not get camli tree revision with %v: %v\n", gitRevCmd.String(), err)
			}
			rev := strings.TrimRight(out, "\n")
			if rev != camliHeadHash {
				// camli tree needs to be updated
				_, err := gitPullCmd.run()
				if err != nil {
					log.Printf("Could not update the Camli repo with %v: %v\n", gitPullCmd.String(), err)
				}
			}
		}
		source = filepath.Join(camliRoot, filepath.FromSlash("misc/buildbot/builder/builder.go"))
	}
	if err := os.Chdir(defaultDir); err != nil {
		log.Fatalf("Could not cd to %v: %v", defaultDir, err)
	}
	tsk := newTask(
		"go",
		"build",
		"-o",
		builderBotBin,
		source,
	)
	if _, err := tsk.run(); err != nil {
		return err
	}
	return nil

}

func startBuilder(goHash, camliHash string) (*exec.Cmd, error) {
	if err := os.Chdir(defaultDir); err != nil {
		log.Fatalf("Could not cd to %v: %v", defaultDir, err)
	}
	dbg.Println("Starting builder bot")
	builderHost := "localhost:" + *builderPort
	ourHost, ourPort, err := net.SplitHostPort(*host)
	if err != nil {
		return nil, fmt.Errorf("Could not find out our host/port: %v", err)
	}
	if ourHost == "0.0.0.0" {
		ourHost = "localhost"
	}
	masterHosts := ourHost + ":" + ourPort
	if *peers != "" {
		masterHosts += "," + *peers
	}
	args := []string{
		"-host",
		builderHost,
		"-masterhosts",
		masterHosts,
	}
	if *builderOpts != "" {
		moreOpts := strings.Split(*builderOpts, ",")
		args = append(args, moreOpts...)
	}
	cmd := exec.Command("./"+builderBotBin, args...)
	cmd.Stdout = multiWriter
	cmd.Stderr = multiWriter
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	inProgresslk.Lock()
	defer inProgresslk.Unlock()
	builderProc = cmd.Process
	inProgress = &testSuite{
		Start:     time.Now(),
		GoHash:    goHash,
		CamliHash: camliHash,
	}
	return cmd, nil
}

var (
	okPrefix       = "/ok/"
	failPrefix     = "/fail/"
	progressPrefix = "/progress"
	currentPrefix  = "/current"
	stderrPrefix   = "/stderr"
	reportPrefix   = "/report"

	statusTpl    = template.Must(template.New("status").Funcs(tmplFuncs).Parse(statusHTML))
	taskTpl      = template.Must(template.New("task").Parse(taskHTML))
	testSuiteTpl = template.Must(template.New("ok").Parse(testSuiteHTML))
)

var tmplFuncs = template.FuncMap{
	"camliRepoURL": camliRepoURL,
	"goRepoURL":    goRepoURL,
	"shortHash":    shortHash,
}

var OSArchVersionTime = regexp.MustCompile(`(.*_.*)/(gotip|go1)/(.*)`)

// unlocked; history needs to be protected from the caller.
func getPastTestSuite(key string) (*testSuite, error) {
	parts := OSArchVersionTime.FindStringSubmatch(key)
	if parts == nil || len(parts) != 4 {
		return nil, fmt.Errorf("bogus osArch/goversion/time url path: %v", key)
	}
	isGoTip := false
	switch parts[2] {
	case "gotip":
		isGoTip = true
	case "go1":
	default:
		return nil, fmt.Errorf("bogus go version in url path: %v", parts[2])
	}
	historyOSArch, ok := history[parts[1]]
	if !ok {
		return nil, fmt.Errorf("os %v not found in history", parts[1])
	}
	for _, v := range historyOSArch {
		ts := v.Go1
		if isGoTip {
			ts = v.GoTip
		}
		if ts.Start.String() == parts[3] {
			return ts, nil
		}
	}
	return nil, fmt.Errorf("date %v not found in history for osArch %v", parts[3], parts[1])
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

func isLocalhost(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// all of this should not happen since addr should be
		// an http.Request.RemoteAddr but never knows...
		addrErr, ok := err.(*net.AddrError)
		if !ok {
			log.Println(err)
			return false
		}
		if addrErr.Err != "missing port in address" {
			log.Println(err)
			return false
		}
		host = addr
	}
	return host == "localhost" || host == "127.0.0.1" || host == "[::1]"
}

func reportHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		log.Println("Invalid method for report handler")
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}
	if !isLocalhost(r.RemoteAddr) {
		dbg.Printf("Refusing remote report from %v for now", r.RemoteAddr)
		http.Error(w, "No remote bot", http.StatusUnauthorized)
		return
	}
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Println("Invalid request for report handler")
		http.Error(w, "Invalid method", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()
	var report struct {
		OSArch string
		Ts     *biTestSuite
	}
	err = json.Unmarshal(body, &report)
	if err != nil {
		log.Printf("Could not decode builder report: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	addTestSuites(report.OSArch, report.Ts)
	fmt.Fprintf(w, "Report ok")
}

func logHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, `<!doctype html>
<html><meta http-equiv="refresh" content="5">
<body><pre>`)
	switch r.URL.Path {
	case stderrPrefix:
		logStderr.Lock()
		_, err := w.Write(logStderr.Bytes())
		logStderr.Unlock()
		if err != nil {
			log.Println("Error serving logStderr:", err)
		}
	default:
		fmt.Fprintln(w, "Unknown log file path passed to logHandler:", r.URL.Path)
		log.Println("Unknown log file path passed to logHandler:", r.URL.Path)
	}
}

func okHandler(w http.ResponseWriter, r *http.Request) {
	t := strings.Replace(r.URL.Path, okPrefix, "", -1)
	historylk.Lock()
	defer historylk.Unlock()
	ts, err := getPastTestSuite(t)
	if err != nil || len(ts.Run) == 0 {
		http.NotFound(w, r)
		return
	}
	lastTask := ts.Run[len(ts.Run)-1]
	lastModTime := lastTask.Start.Add(lastTask.Duration)
	if checkLastModified(w, r, lastModTime) {
		return
	}
	var dat struct {
		BiTs [2]*testSuite
	}
	if ts.IsTip {
		dat.BiTs[1] = ts
	} else {
		dat.BiTs[0] = ts
	}
	err = testSuiteTpl.Execute(w, &dat)
	if err != nil {
		log.Printf("ok template: %v\n", err)
	}
}

func progressHandler(w http.ResponseWriter, r *http.Request) {
	if inProgress == nil {
		http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
		return
	}
	// We only display the progress link and ask for progress for
	// our local builder. The remote ones simply send their full report
	// when they're done.
	resp, err := http.Get("http://localhost:" + *builderPort + "/progress")
	if err != nil {
		log.Printf("Could not get a progress response from builder: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Could not read progress response from builder: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	var ts biTestSuite
	err = json.Unmarshal(body, &ts)
	if err != nil {
		log.Printf("Could not decode builder progress report: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	lastModified = time.Now()
	var dat struct {
		BiTs [2]*testSuite
	}
	dat.BiTs[0] = ts.Go1
	if ts.GoTip != nil && !ts.GoTip.Start.IsZero() {
		dat.BiTs[1] = ts.GoTip
	}
	err = testSuiteTpl.Execute(w, &dat)
	if err != nil {
		log.Printf("progress template: %v\n", err)
	}
}

func failHandler(w http.ResponseWriter, r *http.Request) {
	t := strings.Replace(r.URL.Path, failPrefix, "", -1)
	historylk.Lock()
	defer historylk.Unlock()
	ts, err := getPastTestSuite(t)
	if err != nil || len(ts.Run) == 0 {
		http.NotFound(w, r)
		return
	}
	var failedTask *task
	for _, v := range ts.Run {
		if v.Err != "" {
			failedTask = v
			break
		}
	}
	if failedTask == nil {
		http.NotFound(w, r)
		return
	}
	lastModTime := failedTask.Start.Add(failedTask.Duration)
	if checkLastModified(w, r, lastModTime) {
		return
	}
	failReport := struct {
		TaskErr string
		TsErr   string
	}{
		TaskErr: failedTask.String() + "\n" + failedTask.Err,
		TsErr:   ts.Err,
	}
	err = taskTpl.Execute(w, &failReport)
	if err != nil {
		log.Printf("fail template: %v\n", err)
	}
}

// unprotected read to history, caller needs to lock.
func invertedHistory(OSArch string) (inverted []*biTestSuite) {
	historyOSArch, ok := history[OSArch]
	if !ok {
		return nil
	}
	inverted = make([]*biTestSuite, len(historyOSArch))
	endpos := len(historyOSArch) - 1
	for k, v := range historyOSArch {
		inverted[endpos-k] = v
	}
	return inverted
}

type statusReport struct {
	OSArch   string
	Hs       []*biTestSuite
	Progress *testSuite
}

func statusHandler(w http.ResponseWriter, r *http.Request) {
	historylk.Lock()
	inProgresslk.Lock()
	defer inProgresslk.Unlock()
	defer historylk.Unlock()
	var localOne *statusReport
	if inProgress != nil {
		localOne = &statusReport{
			Progress: &testSuite{
				Start:     inProgress.Start,
				CamliHash: inProgress.CamliHash,
				GoHash:    inProgress.GoHash,
			},
		}
	}
	var reports []*statusReport
	for OSArch, historyOSArch := range history {
		if len(historyOSArch) == 0 {
			continue
		}
		hs := invertedHistory(OSArch)
		if historyOSArch[0].Local {
			if localOne == nil {
				localOne = &statusReport{}
			}
			localOne.OSArch = OSArch
			localOne.Hs = hs
			continue
		}
		reports = append(reports, &statusReport{
			OSArch: OSArch,
			Hs:     hs,
		})
	}
	if localOne != nil {
		reports = append([]*statusReport{localOne}, reports...)
	}
	if checkLastModified(w, r, lastModified) {
		return
	}
	err := statusTpl.Execute(w, reports)
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
	return "https://camlistore.googlesource.com/camlistore/+/" + hash
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
	.pull-right {
		float: right;
	}
	.fail {
		color: #C00;
	}
</style>
`

var statusHTML = `
<!DOCTYPE HTML>
<html>
	<head>
		<title>Camlistore tests Dashboard</title>` +
	styleHTML + `
	</head>
	<body>

	<h1>Camlibot status<span class="pull-right"><a href="` + stderrPrefix + `">stderr</a></span></h1>

	<table class="build">
	<colgroup class="col-hash" span="1"></colgroup>
	<colgroup class="build" span="1"></colgroup>
	<colgroup class="build" span="1"></colgroup>
	<colgroup class="user" span="1"></colgroup>
	<colgroup class="user" span="1"></colgroup>
	<tr>
	<!-- extra row to make alternating colors use dark for first result -->
	</tr>
	{{range $report := .}}
	<tr>
	<th>{{$report.OSArch}}</th>
	<th colspan="1">Go tip hash</th>
	<th colspan="1">Camli HEAD hash</th>
	<th colspan="1">Go1</th>
	<th colspan="1">Gotip</th>
	</tr>
	{{if $report.Progress}}
		<tr class="commit">
			<td class="hash">{{$report.Progress.Start}}</td>
			<td class="hash">
				<a href="{{goRepoURL $report.Progress.GoHash}}">{{shortHash $report.Progress.GoHash}}</a>
			</td>
			<td class="hash">
				<a href="{{camliRepoURL $report.Progress.CamliHash}}">{{shortHash $report.Progress.CamliHash}}</a>
			</td>
			<td class="result" colspan="2">
				<a href="` + progressPrefix + `" class="ok">In progress</a>
			</td>
		</tr>
	{{end}}
	{{if $report.Hs}}
		{{range $bits := $report.Hs}}
			<tr class="commit">
				<td class="hash">{{$bits.Go1.Start}}</td>
				<td class="hash">
					<a href="{{goRepoURL $bits.Go1.GoHash}}">{{shortHash $bits.Go1.GoHash}}</a>
				</td>
				<td class="hash">
					<a href="{{camliRepoURL $bits.Go1.CamliHash}}">{{shortHash $bits.Go1.CamliHash}}</a>
				</td>
				<td class="result">
				{{if $bits.Go1.Err}}
					<a href="` + failPrefix + `{{$report.OSArch}}/go1/{{$bits.Go1.Start}}" class="fail">fail</a>
				{{else}}
					<a href="` + okPrefix + `{{$report.OSArch}}/go1/{{$bits.Go1.Start}}" class="ok">ok</a>
				{{end}}
				</td>
				<td class="result">
				{{if $bits.GoTip}}
					{{if $bits.GoTip.Err}}
						<a href="` + failPrefix + `{{$report.OSArch}}/gotip/{{$bits.GoTip.Start}}" class="fail">fail</a>
					{{else}}
						<a href="` + okPrefix + `{{$report.OSArch}}/gotip/{{$bits.GoTip.Start}}" class="ok">ok</a>
					{{end}}
				{{else}}
					<a href="` + currentPrefix + `" class="ok">In progress</a>
				{{end}}
				</td>
			</tr>
		{{end}}
	{{end}}
	<tr>
	<td colspan="5">&nbsp;</td>
	</tr>
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
	{{range $ts := .BiTs}}
		{{if $ts}}
		<h2> Testsuite for {{if $ts.IsTip}}Go tip{{else}}Go 1{{end}} at {{$ts.Start}} </h2>
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
		{{range $k, $tsk := $ts.Run}}
		<tr>
			<td>{{printf "%v" $tsk}}</td>
			<td>{{$tsk.Duration}}</td>
		</tr>
		{{end}}
		</table>
		{{end}}
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
{{if .TaskErr}}
	<h2>Task:</h2>
	<pre>{{.TaskErr}}</pre>
{{end}}
{{if .TsErr}}
	<h2>Error:</h2>
	<pre>
	{{.TsErr}}
	</pre>
{{end}}
	</body>
</html>
`
