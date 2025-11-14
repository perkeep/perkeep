/*
Copyright 2013 The Perkeep Authors

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
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/blob"
)

// World defines an integration test world.
//
// It's used to run the actual Perkeep binaries (perkeepd,
// pk-put, pk-get, pk, etc) together in large tests, including
// building them, finding them, and wiring them up in an isolated way.
type World struct {
	srcRoot string // typically $GOPATH[0]/src/perkeep.org or just the root dir where go.mod is
	config  string // server config file relative to pkg/test/testdata
	tempDir string
	gobin   string // where the World installs and finds binaries

	addr string // "127.0.0.1:35"

	server    *exec.Cmd
	isRunning int32 // state of the perkeepd server. Access with sync/atomic only.
	serverErr error
}

// NewWorld returns a new test world.
// It uses the GOPATH (explicit or implicit) to find the "perkeep.org" root.
func NewWorld() (*World, error) {
	return WorldFromConfig("server-config.json")
}

// WorldFromConfig returns a new test world based on the given configuration file.
// This cfg is the server config relative to pkg/test/testdata.
// It uses the GOPATH (explicit or implicit) to find the "perkeep.org" root.
func WorldFromConfig(cfg string) (*World, error) {
	root, err := osutil.PkSourceRoot()
	if err != nil {
		return nil, err
	}
	return &World{
		srcRoot: root,
		config:  cfg,
	}, nil
}

func (w *World) Addr() string {
	return w.addr
}

// SourceRoot returns the root of the source tree.
func (w *World) SourceRoot() string {
	return w.srcRoot
}

// Build builds the Perkeep binaries.
func (w *World) Build() error {
	var err error
	w.tempDir, err = os.MkdirTemp("", "perkeep-test-")
	if err != nil {
		return err
	}
	w.gobin = filepath.Join(w.tempDir, "bin")
	if err := os.MkdirAll(w.gobin, 0700); err != nil {
		return err
	}
	// Build.
	{
		cmd := exec.Command("go", "run", "make.go",
			"--stampversion=false",
			"--targets="+strings.Join([]string{
				"perkeep.org/server/perkeepd",
				"perkeep.org/cmd/pk",
				"perkeep.org/cmd/pk-get",
				"perkeep.org/cmd/pk-put",
				"perkeep.org/cmd/pk-mount",
			}, ","))
		if testing.Verbose() {
			// TODO(mpl): do the same when -verbose with devcam test. Even better: see if testing.Verbose
			// can be made true if devcam test -verbose ?
			cmd.Args = append(cmd.Args, "-v=true")
		}
		cmd.Dir = w.srcRoot
		cmd.Env = append(os.Environ(), "GOBIN="+w.gobin)
		log.Print("Running make.go to build perkeep binaries for testing...")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("Error building world: %v, %s", err, string(out))
		}
		if testing.Verbose() {
			log.Printf("%s\n", out)
		}
		log.Print("Ran make.go.")
	}
	return nil
}

// Help outputs the help of perkeepd from the World.
func (w *World) Help() ([]byte, error) {
	if err := w.Build(); err != nil {
		return nil, err
	}
	pkdbin := w.lookPathGobin("perkeepd")
	// Run perkeepd -help.
	cmd := exec.Command(pkdbin, "-help")
	return cmd.CombinedOutput()
}

// Start builds the Perkeep binaries and starts a server.
func (w *World) Start() error {
	if err := w.Build(); err != nil {
		return err
	}
	// Start perkeepd.
	{
		pkdbin := w.lookPathGobin("perkeepd")
		w.server = exec.Command(
			pkdbin,
			"--openbrowser=false",
			"--configfile="+filepath.Join(w.srcRoot, "pkg", "test", "testdata", w.config),
			"--pollparent=true",
			"--listen=127.0.0.1:0",
		)
		var buf bytes.Buffer
		if testing.Verbose() {
			w.server.Stdout = os.Stdout
			w.server.Stderr = os.Stderr
		} else {
			w.server.Stdout = &buf
			w.server.Stderr = &buf
		}

		getPortListener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return err
		}
		defer getPortListener.Close()

		w.server.Dir = w.tempDir
		w.server.Env = append(os.Environ(),
			// "CAMLI_DEBUG=1", // <-- useful for testing
			"CAMLI_MORE_FLAGS=1",
			"CAMLI_ROOT="+w.tempDir,
			"CAMLI_SECRET_RING="+filepath.Join(w.srcRoot, filepath.FromSlash("pkg/jsonsign/testdata/test-secring.gpg")),
			"CAMLI_BASE_URL=http://127.0.0.0:tbd", // filled in later
			"CAMLI_SET_BASE_URL_AND_SEND_ADDR_TO="+getPortListener.Addr().String(),
		)

		if err := w.server.Start(); err != nil {
			w.serverErr = fmt.Errorf("starting perkeepd: %v", err)
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
		upErr := make(chan error, 1)
		go func() {
			c, err := getPortListener.Accept()
			if err != nil {
				upErr <- fmt.Errorf("waiting for child to report its port: %v", err)
				return
			}
			defer c.Close()
			br := bufio.NewReader(c)
			addr, err := br.ReadString('\n')
			if err != nil {
				upErr <- fmt.Errorf("ReadString: %v", err)
				return
			}
			w.addr = strings.TrimSpace(addr)

			for range 100 {
				res, err := http.Get("http://" + w.addr)
				if err == nil {
					res.Body.Close()
					upc <- true
					return
				}
				time.Sleep(50 * time.Millisecond)
			}
			w.serverErr = errors.New(buf.String())
			atomic.StoreInt32(&w.isRunning, 0)
			upErr <- fmt.Errorf("server never became reachable: %v", w.serverErr)
		}()

		select {
		case <-waitc:
			return fmt.Errorf("server exited: %v", w.serverErr)
		case err := <-upErr:
			return err
		case <-upc:
			if err := w.Ping(); err != nil {
				return err
			}
			// Success.
		}
	}
	return nil
}

// Ping returns an error if the world's perkeepd is not running.
func (w *World) Ping() error {
	if atomic.LoadInt32(&w.isRunning) != 1 {
		return fmt.Errorf("perkeepd not running: %v", w.serverErr)
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
	out := MustRunCmd(t, w.Cmd("pk-put", "permanode"))
	br, ok := blob.Parse(strings.TrimSpace(out))
	if !ok {
		t.Fatalf("Expected permanode in pk-put stdout; got %q", out)
	}
	return br
}

func (w *World) PutFile(t *testing.T, name string) blob.Ref {
	out := MustRunCmd(t, w.Cmd("pk-put", "file", name))
	br, ok := blob.Parse(strings.TrimSpace(out))
	if !ok {
		t.Fatalf("Expected blobref in pk-put stdout; got %q", out)
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
	case "pk-get", "pk-put", "pk", "pk-mount":
		// TODO(mpl): lift the pk-put restriction when we have a unified logging mechanism
		if binary == "pk-put" && !hasVerbose() {
			// pk-put and pk are the only ones to have a -verbose flag through cmdmain
			// but pk is never used. (and pk-mount does not even have a -verbose).
			args = append([]string{"-verbose"}, args...)
		}
		binary := w.lookPathGobin(binary)

		cmd = exec.Command(binary, args...)
		clientConfigDir := filepath.Join(w.srcRoot, "config", "dev-client-dir")
		cmd.Env = append(env,
			"CAMLI_CONFIG_DIR="+clientConfigDir,
			// Respected by env expansions in config/dev-client-dir/client-config.json:
			"CAMLI_SERVER="+w.ServerBaseURL(),
			"CAMLI_SECRET_RING="+w.SecretRingFile(),
			"CAMLI_KEYID="+w.ClientIdentity(),
			"CAMLI_AUTH=userpass:testuser:passTestWorld",
		)
	default:
		panic("Unknown binary " + binary)
	}
	return cmd
}

func (w *World) ServerBaseURL() string {
	return fmt.Sprintf("http://%s", w.addr)
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
// pk-put or pk-get command), capturing its stdout for return, and
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
	return filepath.Join(w.srcRoot, "pkg", "jsonsign", "testdata", "test-secring.gpg")
}

// SearchHandlerPath returns the path to the search handler, with trailing slash.
func (w *World) SearchHandlerPath() string { return "/my-search/" }

// ServerBinary returns the location of the perkeepd binary running for this World.
func (w *World) ServerBinary() string {
	return w.lookPathGobin("perkeepd")
}

func (w *World) lookPathGobin(binName string) string {
	if runtime.GOOS == "windows" && !strings.HasSuffix(binName, ".exe") {
		return filepath.Join(w.gobin, binName+".exe")
	}
	return filepath.Join(w.gobin, binName)
}
