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

// Hacks for running camput as a child process on Android.

package main

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/schema"
)

var detectOnce sync.Once
var onAndroidCache bool

func dirExists(f string) bool {
	fi, err := os.Stat(f)
	return err == nil && fi.IsDir()
}

func initOnAndroid() {
	// Good enough heuristic. Suggestions welcome.
	onAndroidCache = dirExists("/data/data") && dirExists("/system/etc")
}

func onAndroid() bool {
	detectOnce.Do(initOnAndroid)
	return onAndroidCache
}

var pingRx = regexp.MustCompile(`\((.+?)\)`)

func androidLookupHost(host string) string {
	// Android has no "dig" or "host" tool, so use "ping -c 1". Ghetto.
	// $ ping -c 1 google.com
	// PING google.com (74.125.224.64) 56(84) bytes of data.
	c := make(chan string, 1)
	go func() {
		cmd := exec.Command("/system/bin/ping", "-c", "1", host)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			panic(err)
		}
		defer stdout.Close()
		if err := cmd.Start(); err != nil {
			log.Printf("Error resolving %q with ping: %v", host, err)
			c <- host
			return
		}
		defer func() {
			if p := cmd.Process; p != nil {
				p.Kill()
			}
		}()
		br := bufio.NewReader(stdout)
		line, err := br.ReadString('\n')
		if err != nil {
			c <- host
			return
		}
		if m := pingRx.FindStringSubmatch(line); m != nil {
			c <- m[1]
			return
		}
		c <- host
	}()
	return <-c
}

func dialFunc() func(network, addr string) (net.Conn, error) {
	if !onAndroid() {
		return nil // use default
	}
	return func(network, addr string) (net.Conn, error) {
		// Temporary laziness hack, avoiding doing a
		// cross-compiled Android cgo build.
		// Without cgo, package net uses
		// /etc/resolv.conf (not available on
		// Android).  We really want a cgo binary to
		// use Android's DNS cache, but it's kinda
		// hard/impossible to cross-compile for now.
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("couldn't split %q", addr)
		}
		return net.Dial(network, net.JoinHostPort(androidLookupHost(host), port))
	}
}

func tlsClientConfig() *tls.Config {
	const certDir = "/system/etc/security/cacerts"
	if fi, err := os.Stat(certDir); err != nil || !fi.IsDir() {
		return nil
	}
	pool := x509.NewCertPool()
	cfg := &tls.Config{RootCAs: pool}

	f, err := os.Open(certDir)
	if err != nil {
		return nil
	}
	defer f.Close()
	names, _ := f.Readdirnames(-1)
	for _, name := range names {
		if pem, err := ioutil.ReadFile(filepath.Join(certDir, name)); err == nil {
			pool.AppendCertsFromPEM(pem)
		}
	}
	return cfg
}

var androidOutput = os.Getenv("CAMPUT_ANDROID_OUTPUT") != ""

// androidStatusRecevier is a blobserver.StatReceiver wrapper that
// reports the full filename path and size of uploaded blobs.
// The android app wrapping camput watches stdout for this, for progress bars.
type androidStatusRecevier struct {
	blobserver.StatReceiver
	path string
}

func (asr androidStatusRecevier) ReceiveBlob(blob *blobref.BlobRef, source io.Reader) (blobref.SizedBlobRef, error) {
	// Sniff the first 1KB of it and don't print the stats if it looks like it was just a schema
	// blob.  We won't update the progress bar for that yet.
	var buf [1024]byte
	contents := buf[:0]
	sb, err := asr.StatReceiver.ReceiveBlob(blob, io.TeeReader(source, writeUntilSliceFull{&contents}))
	if err == nil && !schema.LikelySchemaBlob(contents) {
		fmt.Printf("CHUNK_UPLOADED %d %s %s\n", sb.Size, blob, asr.path)
	}
	return sb, err
}

type writeUntilSliceFull struct {
	s *[]byte
}

func (w writeUntilSliceFull) Write(p []byte) (n int, err error) {
	s := *w.s
	l := len(s)
	growBy := cap(s) - l
	if growBy > len(p) {
		growBy = len(p)
	}
	s = s[0:l+growBy]
	copy(s[l:], p)
	*w.s = s
	return len(p), nil
}

