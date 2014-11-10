/*
Copyright 2013 Google Inc.

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

package test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/osutil"
)

// World defines an integration test world.
//
// It's used to run the actual Camlistore binaries (camlistored,
// camput, camget, camtool, etc) together in large tests, including
// building them, finding them, and wiring them up in an isolated way.
type World struct {
	camRoot  string // typically $GOPATH[0]/src/camlistore.org
	config   string // server config file relative to pkg/test/testdata
	tempDir  string
	listener net.Listener // randomly chosen 127.0.0.1 port for the server
	port     int

	server    *exec.Cmd
	isRunning int32 // state of the camlistored server. Access with sync/atomic only.
	serverErr error

	cammount *os.Process
}

// CamliSourceRoot returns the root of the source tree, or an error.
func camliSourceRoot() (string, error) {
	if os.Getenv("GOPATH") == "" {
		return "", errors.New("GOPATH environment variable isn't set; required to run Camlistore integration tests")
	}
	root, err := osutil.GoPackagePath("camlistore.org")
	if err == os.ErrNotExist {
		return "", errors.New("Directory \"camlistore.org\" not found under GOPATH/src; can't run Camlistore integration tests.")
	}
	return root, nil
}

// NewWorld returns a new test world.
// It requires that GOPATH is set to find the "camlistore.org" root.
func NewWorld() (*World, error) {
	return WorldFromConfig("server-config.json")
}

// WorldFromConfig returns a new test world based on the given configuration file.
// This cfg is the server config relative to pkg/test/testdata.
// It requires that GOPATH is set to find the "camlistore.org" root.
func WorldFromConfig(cfg string) (*World, error) {
	root, err := camliSourceRoot()
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}

	return &World{
		camRoot:  root,
		config:   cfg,
		listener: ln,
		port:     ln.Addr().(*net.TCPAddr).Port,
	}, nil
}

func (w *World) Addr() string {
	return w.listener.Addr().String()
}

// CamliSourceRoot returns the root of the source tree.
func (w *World) CamliSourceRoot() string {
	return w.camRoot
}

// Start builds the Camlistore binaries and starts a server.
func (w *World) Start() error {
	var err error
	w.tempDir, err = ioutil.TempDir("", "camlistore-test-")
	if err != nil {
		return err
	}
	// Build.
	{
		targs := []string{
			"camget",
			"camput",
			"camtool",
			"camlistored",
		}
		// TODO(mpl): investigate why we still rebuild camlistored everytime if run through devcam test.
		// it looks like it's because we always resync the UI files and hence redo the embeds. Next CL.
		var latestModtime time.Time
		for _, target := range targs {
			binPath := filepath.Join(w.camRoot, "bin", target)
			fi, err := os.Stat(binPath)
			if err != nil {
				if !os.IsNotExist(err) {
					return fmt.Errorf("could not stat %v: %v", binPath, err)
				}
			} else {
				modTime := fi.ModTime()
				if modTime.After(latestModtime) {
					latestModtime = modTime
				}
			}
		}
		cmd := exec.Command("go", "run", "make.go",
			fmt.Sprintf("--if_mods_since=%d", latestModtime.Unix()),
		)
		if testing.Verbose() {
			// TODO(mpl): do the same when -verbose with devcam test. Even better: see if testing.Verbose
			// can be made true if devcam test -verbose ?
			cmd.Args = append(cmd.Args, "-v=true")
		}
		cmd.Dir = w.camRoot
		log.Print("Running make.go to build camlistore binaries for testing...")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("Error building world: %v, %s", err, string(out))
		}
		if testing.Verbose() {
			log.Printf("%s\n", out)
		}
		log.Print("Ran make.go.")
	}
	// Start camlistored.
	{
		w.server = exec.Command(
			filepath.Join(w.camRoot, "bin", "camlistored"),
			"--openbrowser=false",
			"--configfile="+filepath.Join(w.camRoot, "pkg", "test", "testdata", w.config),
			"--listen=FD:3",
			"--pollparent=true",
		)
		var buf bytes.Buffer
		if testing.Verbose() {
			w.server.Stdout = os.Stdout
			w.server.Stderr = os.Stderr
		} else {
			w.server.Stdout = &buf
			w.server.Stderr = &buf
		}
		w.server.Dir = w.tempDir
		w.server.Env = append(os.Environ(),
			"CAMLI_DEBUG=1",
			"CAMLI_ROOT="+w.tempDir,
			"CAMLI_SECRET_RING="+filepath.Join(w.camRoot, filepath.FromSlash("pkg/jsonsign/testdata/test-secring.gpg")),
			"CAMLI_BASE_URL=http://127.0.0.1:"+strconv.Itoa(w.port),
		)
		listenerFD, err := w.listener.(*net.TCPListener).File()
		if err != nil {
			return err
		}
		w.server.ExtraFiles = []*os.File{listenerFD}
		if err := w.server.Start(); err != nil {
			w.serverErr = fmt.Errorf("starting camlistored: %v", err)
			return w.serverErr
		}

		atomic.StoreInt32(&w.isRunning, 1)
		waitc := make(chan error, 1)
		go func() {
			err := w.server.Wait()
			w.serverErr = fmt.Errorf("%v: %s", err, buf.String())
			atomic.StoreInt32(&w.isRunning, 0)
			waitc <- w.serverErr
		}()
		upc := make(chan bool)
		timeoutc := make(chan bool)
		go func() {
			for i := 0; i < 100; i++ {
				res, err := http.Get("http://127.0.0.1:" + strconv.Itoa(w.port))
				if err == nil {
					res.Body.Close()
					upc <- true
					return
				}
				time.Sleep(50 * time.Millisecond)
			}
			w.serverErr = errors.New(buf.String())
			atomic.StoreInt32(&w.isRunning, 0)
			timeoutc <- true
		}()

		select {
		case <-waitc:
			return fmt.Errorf("server exited: %v", w.serverErr)
		case <-timeoutc:
			return fmt.Errorf("server never became reachable: %v", w.serverErr)
		case <-upc:
			if err := w.Ping(); err != nil {
				return err
			}
			// Success.
		}
	}
	return nil
}

// Ping returns an error if the world's camlistored is not running.
func (w *World) Ping() error {
	if atomic.LoadInt32(&w.isRunning) != 1 {
		return fmt.Errorf("camlistored not running: %v", w.serverErr)
	}
	return nil
}

func (w *World) Stop() {
	if w == nil {
		return
	}
	if err := w.server.Process.Kill(); err != nil {
		log.Fatalf("killed failed: %v", err)
	}

	if d := w.tempDir; d != "" {
		os.RemoveAll(d)
	}
}

func (w *World) NewPermanode(t *testing.T) blob.Ref {
	if err := w.Ping(); err != nil {
		t.Fatal(err)
	}
	out := MustRunCmd(t, w.Cmd("camput", "permanode"))
	br, ok := blob.Parse(strings.TrimSpace(out))
	if !ok {
		t.Fatalf("Expected permanode in camput stdout; got %q", out)
	}
	return br
}

func (w *World) Cmd(binary string, args ...string) *exec.Cmd {
	return w.CmdWithEnv(binary, os.Environ(), args...)
}

func (w *World) CmdWithEnv(binary string, env []string, args ...string) *exec.Cmd {
	hasVerbose := func() bool {
		for _, v := range args {
			if v == "-verbose" || v == "--verbose" {
				return true
			}
		}
		return false
	}
	var cmd *exec.Cmd
	switch binary {
	case "camget", "camput", "camtool", "cammount":
		// TODO(mpl): lift the camput restriction when we have a unified logging mechanism
		if binary == "camput" && !hasVerbose() {
			// camput and camtool are the only ones to have a -verbose flag through cmdmain
			// but camtool is never used. (and cammount does not even have a -verbose).
			args = append([]string{"-verbose"}, args...)
		}
		cmd = exec.Command(filepath.Join(w.camRoot, "bin", binary), args...)
		clientConfigDir := filepath.Join(w.camRoot, "config", "dev-client-dir")
		cmd.Env = append([]string{
			"CAMLI_CONFIG_DIR=" + clientConfigDir,
			// Respected by env expansions in config/dev-client-dir/client-config.json:
			"CAMLI_SERVER=" + w.ServerBaseURL(),
			"CAMLI_SECRET_RING=" + w.SecretRingFile(),
			"CAMLI_KEYID=" + w.ClientIdentity(),
			"CAMLI_AUTH=userpass:testuser:passTestWorld",
		}, env...)
	default:
		panic("Unknown binary " + binary)
	}
	return cmd
}

func (w *World) ServerBaseURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", w.port)
}

var theWorld *World

// GetWorld returns (creating if necessary) a test singleton world.
// It calls Fatal on the provided test if there are problems.
func GetWorld(t *testing.T) *World {
	w := theWorld
	if w == nil {
		var err error
		w, err = NewWorld()
		if err != nil {
			t.Fatalf("Error finding test world: %v", err)
		}
		err = w.Start()
		if err != nil {
			t.Fatalf("Error starting test world: %v", err)
		}
		theWorld = w
	}
	return w
}

// GetWorldMaybe returns the current World. It might be nil.
func GetWorldMaybe(t *testing.T) *World {
	return theWorld
}

// RunCmd runs c (which is assumed to be something short-lived, like a
// camput or camget command), capturing its stdout for return, and
// also capturing its stderr, just in the case of errors.
// If there's an error, the return error fully describes the command and
// all output.
func RunCmd(c *exec.Cmd) (output string, err error) {
	var stdout, stderr bytes.Buffer
	if testing.Verbose() {
		c.Stderr = io.MultiWriter(os.Stderr, &stderr)
		c.Stdout = io.MultiWriter(os.Stdout, &stdout)
	} else {
		c.Stderr = &stderr
		c.Stdout = &stdout
	}
	err = c.Run()
	if err != nil {
		return "", fmt.Errorf("Error running command %+v: Stdout:\n%s\nStderr:\n%s\n", c, stdout.String(), stderr.String())
	}
	return stdout.String(), nil
}

// MustRunCmd wraps RunCmd, failing t if RunCmd returns an error.
func MustRunCmd(t testing.TB, c *exec.Cmd) string {
	out, err := RunCmd(c)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// ClientIdentity returns the GPG identity to use in World tests, suitable
// for setting in CAMLI_KEYID.
func (w *World) ClientIdentity() string {
	return "26F5ABDA"
}

// SecretRingFile returns the GnuPG secret ring, suitable for setting
// in CAMLI_SECRET_RING.
func (w *World) SecretRingFile() string {
	return filepath.Join(w.camRoot, "pkg", "jsonsign", "testdata", "test-secring.gpg")
}

// SearchHandlerPath returns the path to the search handler, with trailing slash.
func (w *World) SearchHandlerPath() string { return "/my-search/" }
