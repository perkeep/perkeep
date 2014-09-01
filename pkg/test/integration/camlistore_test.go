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

package integration

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"camlistore.org/pkg/test"
	"camlistore.org/third_party/github.com/gorilla/websocket"
)

// Test that running:
//   $ camput permanode
// ... creates and uploads a permanode, and that we can camget it back.
func TestCamputPermanode(t *testing.T) {
	w := test.GetWorld(t)
	br := w.NewPermanode(t)

	out := test.MustRunCmd(t, w.Cmd("camget", br.String()))
	mustHave := []string{
		`{"camliVersion": 1,`,
		`"camliSigner": "`,
		`"camliType": "permanode",`,
		`random": "`,
		`,"camliSig":"`,
	}
	for _, str := range mustHave {
		if !strings.Contains(out, str) {
			t.Errorf("Expected permanode response to contain %q; it didn't. Got: %s", str, out)
		}
	}
}

func TestWebsocketQuery(t *testing.T) {
	w := test.GetWorld(t)
	pn := w.NewPermanode(t)
	test.MustRunCmd(t, w.Cmd("camput", "attr", pn.String(), "tag", "foo"))

	check := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}

	const bufSize = 1 << 20

	c, err := net.Dial("tcp", w.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	wc, _, err := websocket.NewClient(c, &url.URL{Host: w.Addr(), Path: w.SearchHandlerPath() + "ws"}, nil, bufSize, bufSize)
	check(err)

	msg, err := wc.NextWriter(websocket.TextMessage)
	check(err)

	_, err = msg.Write([]byte(`{"tag": "foo", "query": { "expression": "tag:foo" }}`))
	check(err)
	check(msg.Close())

	errc := make(chan error, 1)
	go func() {
		inType, inMsg, err := wc.ReadMessage()
		if err != nil {
			errc <- err
			return
		}
		if !strings.HasPrefix(string(inMsg), `{"tag":"_status"`) {
			errc <- fmt.Errorf("unexpected message type=%d msg=%q, wanted status update", inType, inMsg)
			return
		}
		inType, inMsg, err = wc.ReadMessage()
		if err != nil {
			errc <- err
			return
		}
		if strings.Contains(string(inMsg), pn.String()) {
			errc <- nil
			return
		}
		errc <- fmt.Errorf("unexpected message type=%d msg=%q", inType, inMsg)
	}()
	select {
	case err := <-errc:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(5 * time.Second):
		t.Error("timeout")
	}
}

func TestInternalHandler(t *testing.T) {
	w := test.GetWorld(t)
	tests := map[string]int{
		"/no-http-storage/":                                                    401,
		"/no-http-handler/":                                                    401,
		"/good-status/":                                                        200,
		"/bs-and-maybe-also-index/camli":                                       400,
		"/bs/camli/sha1-b2201302e129a4396a323cb56283cddeef11bbe8":              404,
		"/no-http-storage/camli/sha1-b2201302e129a4396a323cb56283cddeef11bbe8": 401,
	}
	for suffix, want := range tests {
		res, err := http.Get(w.ServerBaseURL() + suffix)
		if err != nil {
			t.Fatalf("On %s: %v", suffix, err)
		}
		if res.StatusCode != want {
			t.Errorf("For %s: Status = %d; want %d", suffix, res.StatusCode, want)
		}
		res.Body.Close()
	}
}

func mustTempDir(t *testing.T) (name string, cleanup func()) {
	dir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatal(err)
	}
	return dir, func() { os.RemoveAll(dir) }
}

func mustWriteFile(t *testing.T, path, contents string) {
	err := ioutil.WriteFile(path, []byte(contents), 0644)
	if err != nil {
		t.Fatal(err)
	}
}

// Run camput in the environment it runs in under the Android app.
// This matches how camput is used in UploadThread.java.
func TestAndroidCamputFile(t *testing.T) {
	w := test.GetWorld(t)
	// UploadThread.java sets:
	//   CAMLI_AUTH (set by w.CmdWithEnv)
	//   CAMLI_TRUSTED_CERT (not needed)
	//   CAMLI_CACHE_DIR
	//   CAMPUT_ANDROID_OUTPUT=1
	cacheDir, clean := mustTempDir(t)
	defer clean()
	env := []string{
		"CAMPUT_ANDROID_OUTPUT=1",
		"CAMLI_CACHE_DIR=" + cacheDir,
	}
	cmd := w.CmdWithEnv("camput",
		env,
		"--server="+w.ServerBaseURL(),
		"file",
		"-stdinargs",
		"-vivify")
	cmd.Stderr = os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Ping(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()

	srcDir, clean := mustTempDir(t)
	defer clean()

	file1 := filepath.Join(srcDir, "file1.txt")
	mustWriteFile(t, file1, "contents 1")
	file2 := filepath.Join(srcDir, "file2.txt")
	mustWriteFile(t, file2, "contents 2 longer length")

	go func() {
		fmt.Fprintf(in, "%s\n", file1)
		fmt.Fprintf(in, "%s\n", file2)
	}()

	waitc := make(chan error)
	go func() {
		sc := bufio.NewScanner(out)
		fileUploaded := 0
		for sc.Scan() {
			t.Logf("Got: %q", sc.Text())
			f := strings.Fields(sc.Text())
			if len(f) == 0 {
				t.Logf("empty text?")
				continue
			}
			if f[0] == "FILE_UPLOADED" {
				fileUploaded++
				if fileUploaded == 2 {
					break
				}
			}
		}
		in.Close()
		if err := sc.Err(); err != nil {
			t.Error(err)
		}
	}()

	defer cmd.Process.Kill()
	go func() {
		waitc <- cmd.Wait()
	}()
	select {
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for camput to end")
	case err := <-waitc:
		if err != nil {
			t.Errorf("camput exited uncleanly: %v", err)
		}
	}
}
