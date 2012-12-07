package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	maxInterval = 24 * time.Hour
	warmup      = 17 * time.Second // duration before we test if dev-server has started properly
)

var (
	debug    = flag.Bool("debug", false, "print debug statements")
	gotipdir = flag.String("gotip", "./gotip", "path to the Go tip tree")
	help     = flag.Bool("h", false, "show this help")
	host     = flag.String("host", "0.0.0.0:8080", "listening hostname and port")
	nobuild  = flag.Bool("nobuild", false, "skip building Go tip, mostly for debugging")
)

var (
	cachedCamliRoot string
	camliRoot       string
	currentTask     task
	dbg             *debugger
	defaultDir      string
	gopath          string
	interval        time.Duration // frequency at which the tests are run
	lastErr         error
)

func usage() {
	fmt.Fprintf(os.Stderr, "\t buildbot \n")
	flag.PrintDefaults()
	os.Exit(2)
}

func setup() {
	var err error
	if _, err := os.Stat(*gotipdir); err != nil {
		log.Fatalf("Problem with Go tip dir: %v", err)
	}
	defaultDir, err = os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	interval = time.Second
	dbg = &debugger{log.New(os.Stderr, "", log.LstdFlags)}

	// setup temp gopath
	gopath, err = ioutil.TempDir("", "camlibot-gopath")
	if err != nil {
		log.Fatalf("problem with tempdir: %v", err)
	}
	srcDir := filepath.Join(gopath, "src")
	err = os.Mkdir(srcDir, 0755)
	if err != nil {
		log.Fatalf("problem with src dir: %v", err)
	}
	err = os.Setenv("GOPATH", gopath)
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
		setCurrentTask("setup",
			"git clone https://camlistore.org/r/p/camlistore "+camliRoot)
		_, err = runCmd(getCurrentTask().cmd)
		if err != nil {
			log.Fatalf("problem with git clone: %v", err)
		}
	} else {
		// get cache, purge it, update it.
		err = os.Rename(cachedCamliRoot, camliRoot)
		if err != nil {
			log.Fatal(err)
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
		setCurrentTask("setup", "git reset --hard HEAD")
		_, err = runCmd(getCurrentTask().cmd)
		if err != nil {
			log.Fatalf("problem with git reset: %v", err)
		}
		setCurrentTask("setup", "git clean -Xdf")
		_, err = runCmd(getCurrentTask().cmd)
		if err != nil {
			log.Fatalf("problem with git clean: %v", err)
		}
		setCurrentTask("setup", "git pull")
		_, err = runCmd(getCurrentTask().cmd)
		if err != nil {
			log.Fatalf("problem with git pull: %v", err)
		}
	}
}

func cleanTempGopath() {
	err := os.Rename(camliRoot, cachedCamliRoot)
	if err != nil {
		panic(err)
	}
	err = os.RemoveAll(gopath)
	if err != nil {
		panic(err)
	}
}

func handleSignals() {
	c := make(chan os.Signal, 1)
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
			log.Printf("SIGINT: cleaning up %v before terminating", gopath)
			cleanTempGopath()
			os.Exit(0)
		default:
			panic("should not get other signals here")
		}
	}
}

// TODO(mpl): history of previous commands and their durations
func reportStatus(w http.ResponseWriter, r *http.Request) {
	taskStr := "Current task: " + getCurrentTask().name
	errStr := ""
	if lastErr != nil {
		errStr = fmt.Sprintf("Last error: %v", lastErr)
	}
	fmt.Fprintf(w, "%s\n%s", taskStr, errStr)
}

func increaseInterval() {
	if interval < maxInterval {
		interval = interval * 2
	}
}

type debugger struct {
	lg *log.Logger
}

func (dbg *debugger) Printf(format string, v ...interface{}) {
	if *debug {
		dbg.lg.Printf(format, v)
	}
}

func (dbg *debugger) Println(v ...interface{}) {
	if *debug {
		dbg.lg.Println(v)
	}
}

type task struct {
	lk   sync.Mutex
	name string
	// actual command that is run for this task. it includes the command and
	// all the arguments, space separated. shell metacharacters not supported.
	cmd string
}

func getCurrentTask() *task {
	currentTask.lk.Lock()
	defer currentTask.lk.Unlock()
	return &task{name: currentTask.name, cmd: currentTask.cmd}
}

func setCurrentTask(name, cmd string) {
	currentTask.lk.Lock()
	defer currentTask.lk.Unlock()
	currentTask.name = name
	currentTask.cmd = cmd
}

func runCmd(tsk string) (string, error) {
	dbg.Println(getCurrentTask().cmd)
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
		return "", fmt.Errorf("error with %v: %v\n", getCurrentTask().cmd, stderr.String())
	}
	return stdout.String(), nil
}

func buildGoTip() error {
	if *nobuild {
		return nil
	}
	err := os.Chdir(*gotipdir)
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
		"hg update -C default",
		"hg --config extensions.purge= purge --all",
		"./make.bash",
	}
	for i := 0; i < 2; i++ {
		setCurrentTask("building go tip", tasks[i])
		_, err = runCmd(getCurrentTask().cmd)
		if err != nil {
			return err
		}
	}
	err = os.Chdir("src")
	if err != nil {
		log.Fatal(err)
	}
	setCurrentTask("building go tip", tasks[2])
	_, err = runCmd(getCurrentTask().cmd)
	if err != nil {
		return err
	}
	return nil
}

func buildCamli() error {
	if e := os.Getenv("GOARCH"); e == "" {
		log.Fatal("GOARCH not set")
	}
	if e := os.Getenv("GOOS"); e == "" {
		log.Fatal("GOOS not set")
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

	tasks := []string{
		"make",
		"make presubmit",
	}
	for _, v := range tasks {
		setCurrentTask("building camlistore", v)
		_, err := runCmd(v)
		if err != nil {
			return err
		}
	}
	return nil
}

func runCamli() (*os.Process, error) {
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

	setCurrentTask("starting camlistore", "./dev-server --wipe --mysql")
	dbg.Println(getCurrentTask().cmd)
	fields := strings.Fields(getCurrentTask().cmd)
	args := fields[1:]
	cmd := exec.Command(fields[0], args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	cerr := make(chan error)
	go func() {
		cerr <- cmd.Run()
	}()
	time.Sleep(warmup)
	select {
	case <-cerr:
		dbg.Println("dev server DEAD")
		return nil, fmt.Errorf("%v: %v\n", getCurrentTask().cmd, "camlistored terminated prematurely")
	default:
		dbg.Println("dev server OK")
		break
	}
	return cmd.Process, nil
}

// TODO(mpl): maybe killall or such to be sure
func killCamli(proc *os.Process) {
	dbg.Println("killing dev server")
	err := proc.Kill()
	if err != nil {
		log.Fatalf("Could not kill dev-server: %v", err)
	}
	dbg.Println("")
}

func hitURL(url string) error {
	setCurrentTask("Hitting camli", fmt.Sprintf("http.Get(\"%s\")", url))
	dbg.Println(getCurrentTask().cmd)
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("%v: %v\n", getCurrentTask().cmd, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("%v, got StatusCode: %d\n", getCurrentTask().cmd, resp.StatusCode)
	}
	return nil
}

func hitCamli() error {
	urls := []string{
		"http://localhost:3179/ui/",
		"http://localhost:3179/ui/new/home.html",
	}
	for _, v := range urls {
		err := hitURL(v)
		if err != nil {
			return err
		}
	}
	return nil
}

func camliClients() error {
	fileName := "AUTHORS"
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

	// push the file to camli
	setCurrentTask("camput", "./dev-camput file --permanode "+fileName)
	out, err := runCmd(getCurrentTask().cmd)
	if err != nil {
		return err
	}
	// TODO(mpl): parsing camput output is kinda weak.
	firstSHA1 := regexp.MustCompile(`.*(sha1-[a-zA-Z0-9]+)\nsha1-[a-zA-Z0-9]+\nsha1-[a-zA-Z0-9]+\n.*`)
	m := firstSHA1.FindStringSubmatch(out)
	if m == nil {
		return fmt.Errorf("%v: unexpected camput output\n", getCurrentTask().cmd)
	}
	blobref := m[1]

	// get the file's json to find out the file's blobref
	setCurrentTask("camget", "./dev-camget "+blobref)
	out, err = runCmd(getCurrentTask().cmd)
	if err != nil {
		return err
	}
	blobrefPattern := regexp.MustCompile(`"blobRef": "(sha1-[a-zA-Z0-9]+)",\n.*`)
	m = blobrefPattern.FindStringSubmatch(out)
	if m == nil {
		return fmt.Errorf("%v: unexpected camget output\n", getCurrentTask().cmd)
	}
	blobref = m[1]

	// finally, get the file back
	setCurrentTask("camget", "./dev-camget "+blobref)
	out, err = runCmd(getCurrentTask().cmd)
	if err != nil {
		return err
	}

	// and compare it with the original
	fileContents, err := ioutil.ReadFile(fileName)
	if err != nil {
		log.Fatalf("Could not read %v: %v", fileName, err)
	}
	if string(fileContents) != out {
		return fmt.Errorf("%v: contents fetched with camget differ from %v contents", getCurrentTask().cmd, fileName)
	}
	return nil
}

func hitMySQL() error {
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
	setCurrentTask("mysql", "./dev-camput file --filenodes "+filepath.Join(camliRoot, "pkg"))
	_, err = runCmd(getCurrentTask().cmd)
	if err != nil {
		return err
	}
	return nil
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

	http.HandleFunc("/", reportStatus)
	go http.ListenAndServe(*host, nil)

	for {
		setCurrentTask("sleeping", fmt.Sprintf("time.Sleep(%d)", interval))
		dbg.Println(getCurrentTask().cmd)
		time.Sleep(interval)
		increaseInterval()
		err := buildGoTip()
		if err != nil {
			lastErr = err
			dbg.Printf("%v", lastErr)
			continue
		}
		err = buildCamli()
		if err != nil {
			lastErr = err
			dbg.Printf("%v", lastErr)
			continue
		}
		proc, err := runCamli()
		if err != nil {
			lastErr = err
			dbg.Printf("%v", lastErr)
			continue
		}
		err = hitCamli()
		if err != nil {
			lastErr = err
			dbg.Printf("%v", lastErr)
			killCamli(proc)
			continue
		}
		err = camliClients()
		if err != nil {
			lastErr = err
			dbg.Printf("%v", lastErr)
			killCamli(proc)
			continue
		}
		err = hitMySQL()
		if err != nil {
			lastErr = err
			dbg.Printf("%v", lastErr)
			killCamli(proc)
			continue
		}

		dbg.Println("All good.")
		killCamli(proc)
		lastErr = nil
		interval = time.Second
		setCurrentTask("sleeping", fmt.Sprintf("time.Sleep(%d)", maxInterval))
		dbg.Println(getCurrentTask().cmd)
		time.Sleep(maxInterval)
	}
}
