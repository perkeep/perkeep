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
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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
	tempDir  string
	listener net.Listener // randomly chosen 127.0.0.1 port for the server
	port     int

	server   *exec.Cmd
	cammount *os.Process
}

// NewWorld returns a new test world.
// It requires that GOPATH is set to find the "camlistore.org" root.
func NewWorld() (*World, error) {
	if os.Getenv("GOPATH") == "" {
		return nil, errors.New("GOPATH environment variable isn't set; required to run Camlistore integration tests")
	}
	root, err := osutil.GoPackagePath("camlistore.org")
	if err == os.ErrNotExist {
		return nil, errors.New("Directory \"camlistore.org\" not found under GOPATH/src; can't run Camlistore integration tests.")
	}
	if err != nil {
		return nil, fmt.Errorf("Error searching for \"camlistore.org\" under GOPATH: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}

	return &World{
		camRoot:  root,
		listener: ln,
		port:     ln.Addr().(*net.TCPAddr).Port,
	}, nil
}

func (w *World) Addr() string {
	return w.listener.Addr().String()
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
		cmd := exec.Command("go", "run", "make.go")
		cmd.Dir = w.camRoot
		log.Print("Running make.go to build camlistore binaries for testing...")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("Error building world: %v, %s", err, string(out))
		}
		log.Print("Ran make.go.")
	}

	// Start camlistored.
	{
		w.server = exec.Command(
			filepath.Join(w.camRoot, "bin", "camlistored"),
			"--openbrowser=false",
			"--configfile="+filepath.Join(w.camRoot, "pkg", "test", "testdata", "server-config.json"),
			"--listen=FD:3",
			"--pollparent=true",
		)
		var buf bytes.Buffer
		w.server.Stdout = &buf
		w.server.Stderr = &buf
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
			return fmt.Errorf("Starting camlistored: %v", err)
		}
		waitc := make(chan error, 1)
		go func() {
			waitc <- w.server.Wait()
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
			timeoutc <- true
		}()

		select {
		case err := <-waitc:
			return fmt.Errorf("server exited: %v: %s", err, buf.String())
		case <-timeoutc:
			return errors.New("server never became reachable")
		case <-upc:
			// Success.
		}
	}
	return nil
}

func (w *World) Stop() {
	if w == nil {
		return
	}
	w.server.Process.Kill()

	if d := w.tempDir; d != "" {
		os.RemoveAll(d)
	}
}

func (w *World) NewPermanode(t *testing.T) blob.Ref {
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
	cmd := exec.Command(filepath.Join(w.camRoot, "bin", binary), args...)
	switch binary {
	case "camget", "camput", "camtool", "cammount":
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
	c.Stderr = &stderr
	c.Stdout = &stdout
	err = c.Run()
	if err != nil {
		return "", fmt.Errorf("Error running command %+v: Stdout:\n%s\nStderrr:\n%s\n", c, stdout.String(), stderr.String())
	}
	return stdout.String(), nil
}

// MustRunCmd wraps RunCmd, failing t if RunCmd returns an error.
func MustRunCmd(t *testing.T, c *exec.Cmd) string {
	out, err := RunCmd(c)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// ClientIdentity returns the GPG identity to use in World tests, suitable
// for setting in CAMLI_CLIENT_IDENTITY.
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
