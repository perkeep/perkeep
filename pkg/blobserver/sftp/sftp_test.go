/*
Copyright 2018 The Perkeep Authors

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

package sftp

import (
	"encoding/json"
	"flag"
	"net"
	"os"
	"path"
	"sort"
	"strings"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/storagetest"
)

var runBrokenTests = flag.Bool("run-broken-tests", false, "run known-broken tests")

const testEnvKey = "PK_SFTP_TEST_AUTH_JSON"

func testSFTPServer(t *testing.T, root string, handleConn func(net.Conn) error) {
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			if c, err := ln.Accept(); err != nil {
				return
			} else {
				if err := handleConn(c); err != nil {
					t.Errorf("handleConn: %+v", err)
				}
			}
		}
	}()
	storagetest.Test(t, func(t *testing.T) blobserver.Storage {
		sto, err := NewStorage(ln.Addr().String(), root, &ssh.ClientConfig{User: "RAWSFTPNOSSH"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ln.Close() })
		return sto
	})
}

func TestStorage_Memory(t *testing.T) {
	if !*runBrokenTests {
		t.Skip("skipping; the sftp package's in-memory Handler server seems to not work correctly")
	}
	testSFTPServer(t, ".", func(c net.Conn) error {
		return sftp.NewRequestServer(c, sftp.InMemHandler()).Serve()
	})
}

func TestStorage_TempDir(t *testing.T) {
	td, err := os.MkdirTemp("", "sftptest")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(td)
	testSFTPServer(t, td, func(c net.Conn) error {
		srv, err := sftp.NewServer(c)
		if err != nil {
			return err
		}
		return srv.Serve()
	})
}

func TestStorage_Manual(t *testing.T) {
	sftpTestAuthFile := os.Getenv(testEnvKey)
	if sftpTestAuthFile == "" {
		t.Skipf("skipping integration test when %s not set to path to JSON file of config for testing", testEnvKey)
	}
	jconf, err := os.ReadFile(sftpTestAuthFile)
	if err != nil {
		t.Fatal(err)
	}

	storagetest.Test(t, func(t *testing.T) blobserver.Storage {
		conf := make(map[string]any)
		if err := json.Unmarshal(jconf, &conf); err != nil {
			t.Fatalf("Error parsing JSON file %s: %v", sftpTestAuthFile, err)
		}
		sto, err := newFromConfig(nil, conf)
		if err != nil {
			t.Fatalf("newFromConfig error in file %s: %v", sftpTestAuthFile, err)
		}
		s := sto.(*Storage)
		if s.cc.User != "pktest" && s.cc.User != "RAWSFTPNOSSH" && !strings.Contains(s.root, "test") {
			t.Fatalf("expected JSON config's user to be one of {pktest, RAWSFTPNOSSH}, or have any user and root 'dir' containing 'test'")
		}

		// Clean-up code, to run before & after the run.
		clean := func() {
			var toDelete []string
			sc, err := s.sftp()
			if err != nil {
				t.Fatalf("getting sftp client for pre/post clean: %v", err)
			}
			walker := sc.Walk(path.Clean(s.root))
			for walker.Step() {
				if err := walker.Err(); err != nil {
					t.Fatalf("walking remote: %v", err)
				}
				p := walker.Path()
				if p == s.root {
					continue
				}
				fi := walker.Stat()
				// We want to delete everything under
				// the root, but be a bit extra
				// paranoid in case their config is
				// busted (despite the pktest/test
				// checks above) and only delete stuff
				// we think that we made:
				if fi.IsDir() || strings.HasPrefix(path.Base(p), "sha") {
					toDelete = append(toDelete, p)
				}
			}
			sort.Slice(toDelete, func(i, j int) bool {
				return len(toDelete[i]) > len(toDelete[j])
			})
			for _, f := range toDelete {
				if err = sc.Remove(f); err != nil {
					t.Errorf("remove %q: %v", f, err)
				}
			}
		}
		clean()
		t.Cleanup(clean)
		return s
	})
}
