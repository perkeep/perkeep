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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/test"
)

// Test that running:
//   $ camput permanode
// ... creates and uploads a permanode, and that we can camget it back.
func TestCamputPermanode(t *testing.T) {
	w := test.GetWorld(t)
	out := test.MustRunCmd(t, w.Cmd("camput", "permanode"))
	br, ok := blob.Parse(strings.TrimSpace(out))
	if !ok {
		t.Fatalf("Expected permanode in stdout; got %q", out)
	}

	out = test.MustRunCmd(t, w.Cmd("camget", br.String()))
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
