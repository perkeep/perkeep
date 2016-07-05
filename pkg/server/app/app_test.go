/*
Copyright 2014 The Camlistore Authors

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

package app

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"testing"
)

func TestRandListen(t *testing.T) {
	tests := []struct {
		randPort       int
		listen         string
		wantListenAddr string
	}{
		{
			listen:         ":3179",
			randPort:       58094,
			wantListenAddr: ":58094",
		},

		{
			listen:         "foo.com:3179",
			randPort:       58094,
			wantListenAddr: "foo.com:58094",
		},
	}
	for _, v := range tests {
		listenAddr, err := randListenFn(v.listen, func() (int, error) { return v.randPort, nil })
		if err != nil {
			t.Error(err)
			continue
		}
		if listenAddr != v.wantListenAddr {
			t.Errorf("for listen addr, got: %v, want: %v", listenAddr, v.wantListenAddr)
		}
	}
}

func TestBaseURL(t *testing.T) {
	tests := []struct {
		listen         string
		baseURL        string
		wantBackendURL string
	}{
		{
			listen:         ":3179",
			baseURL:        "http://foo.com",
			wantBackendURL: "http://foo.com:3179/",
		},

		{
			listen:         "localhost",
			baseURL:        "http://foo.com",
			wantBackendURL: "http://foo.com:80/",
		},

		{
			listen:         "localhost",
			baseURL:        "https://foo.com",
			wantBackendURL: "https://foo.com:443/",
		},

		{
			listen:         "localhost:3179",
			baseURL:        "http://foo.com",
			wantBackendURL: "http://foo.com:3179/",
		},

		{
			listen:         "localhost:3179",
			baseURL:        "https://foo.com",
			wantBackendURL: "https://foo.com:3179/",
		},

		{
			listen:         ":3179",
			baseURL:        "http://foo.com:123",
			wantBackendURL: "http://foo.com:3179/",
		},

		{
			listen:         "localhost",
			baseURL:        "http://foo.com:123",
			wantBackendURL: "http://foo.com:80/",
		},

		{
			listen:         "localhost",
			baseURL:        "https://foo.com:123",
			wantBackendURL: "https://foo.com:443/",
		},

		{
			listen:         "localhost:3179",
			baseURL:        "http://foo.com:123",
			wantBackendURL: "http://foo.com:3179/",
		},

		{
			listen:         "localhost:3179",
			baseURL:        "https://foo.com:123",
			wantBackendURL: "https://foo.com:3179/",
		},
	}
	for _, v := range tests {
		backendURL, err := baseURL(v.baseURL, v.listen)
		if err != nil {
			t.Error(err)
			continue
		}
		if v.wantBackendURL != backendURL {
			t.Errorf("For backendURL, got: %v, want %v", v.wantBackendURL, backendURL)
		}
	}
}

// We just want a helper command that ignores SIGINT.
func ignoreInterrupt() (*os.Process, error) {
	script := `trap "echo hello" SIGINT
echo READY
sleep 10000`
	cmd := exec.Command("bash")

	w, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("couldn't get pipe for helper shell")
	}
	go io.WriteString(w, script)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("couldn't get pipe for helper shell")
	}

	err = cmd.Start()
	if err != nil {
		return nil, fmt.Errorf("couldn't start helper shell")
	}

	r := bufio.NewReader(stdout)
	l, err := r.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("couldn't read from helper shell")
	}
	if string(l) != "READY\n" {
		return nil, fmt.Errorf("unexpected output from helper shell script")
	}
	return cmd.Process, nil
}

func TestQuit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	cmd := exec.Command("sleep", "10000")
	err := cmd.Start()
	if err != nil {
		t.Skip("couldn't run test helper command")
	}
	h := Handler{
		process: cmd.Process,
	}
	err = h.Quit()
	if err != nil {
		t.Errorf("got %v, wanted %v", err, nil)
	}

	pid, err := ignoreInterrupt()
	if err != nil {
		t.Skip("couldn't run test helper command: %v", err)
	}
	h = Handler{
		process: pid,
	}
	err = h.Quit()
	if err != errProcessTookTooLong {
		t.Errorf("got %v, wanted %v", err, errProcessTookTooLong)
	}
}
