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
	"time"

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

type namedInt struct {
	name string
	sync.Mutex
	val int64
}

func (ni *namedInt) Incr(delta int64) {
	ni.Lock()
	ni.val += delta
	nv := ni.val
	ni.Unlock()
	androidf("STAT %s %d\n", ni.name, nv)
}

var (
	statTCPStart   = &namedInt{name: "tcp_start"}
	statTCPStarted = &namedInt{name: "tcp_started"}
	statTCPFail    = &namedInt{name: "tcp_fail"}
	statTCPDone    = &namedInt{name: "tcp_done"}
	statTCPWrites  = &namedInt{name: "tcp_write_byte"}
	statTCPReads   = &namedInt{name: "tcp_read_byte"}
	statDNSStart   = &namedInt{name: "dns_start"}
	statDNSDone    = &namedInt{name: "dns_done"}
)

type statTrackingConn struct {
	net.Conn
}

func (c statTrackingConn) Write(p []byte) (n int, err error) {
	n, err = c.Conn.Write(p)
	statTCPWrites.Incr(int64(n))
	return
}

func (c statTrackingConn) Read(p []byte) (n int, err error) {
	n, err = c.Conn.Read(p)
	statTCPReads.Incr(int64(n))
	return
}

var (
	dnsMu    sync.Mutex
	dnsCache = make(map[string]string)
)

func androidLookupHost(host string) string {
	dnsMu.Lock()
	v, ok := dnsCache[host]
	dnsMu.Unlock()
	if ok {
		return v
	}
	statDNSStart.Incr(1)
	defer statDNSDone.Incr(1)

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
			ip := m[1]
			dnsMu.Lock()
			dnsCache[host] = ip
			dnsMu.Unlock()
			c <- ip
			return
		}
		c <- host
	}()
	return <-c
	return v
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
		statTCPStart.Incr(1)
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			statTCPFail.Incr(1)
			return nil, fmt.Errorf("couldn't split %q", addr)
		}
		c, err := net.Dial(network, net.JoinHostPort(androidLookupHost(host), port))
		if err != nil {
			statTCPFail.Incr(1)
			return nil, err
		}
		statTCPStarted.Incr(1)
		return statTrackingConn{Conn: c}, err
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

var androidOutMu sync.Mutex

func androidf(format string, args ...interface{}) {
	androidOutMu.Lock()
	defer androidOutMu.Unlock()
	fmt.Printf(format, args...)
}

func noteFileUploaded(fullPath string) {
	if !androidOutput {
		return
	}
	androidf("FILE_UPLOADED %s\n", fullPath)
}

type allStats struct {
	total, skipped, uploaded stats
}

var lastStatBroadcast allStats

func printAndroidCamputStatus(t *TreeUpload) {
	bcast := allStats{t.total, t.skipped, t.uploaded}
	if bcast == lastStatBroadcast {
		return
	}
	lastStatBroadcast = bcast

	androidf("STATS nfile=%d nbyte=%d skfile=%d skbyte=%d upfile=%d upbyte=%d\n",
		t.total.files, t.total.bytes,
		t.skipped.files, t.skipped.bytes,
		t.uploaded.files, t.uploaded.bytes)
}

// androidStatusRecevier is a blobserver.StatReceiver wrapper that
// reports the full filename path and size of uploaded blobs.
// The android app wrapping camput watches stdout for this, for progress bars.
type androidStatusRecevier struct {
	sr   blobserver.StatReceiver
	path string
}

func (asr androidStatusRecevier) noteChunkOnServer(sb blobref.SizedBlobRef) {
	androidf("CHUNK_UPLOADED %d %s %s\n", sb.Size, sb.BlobRef, asr.path)
}

func (asr androidStatusRecevier) ReceiveBlob(blob *blobref.BlobRef, source io.Reader) (blobref.SizedBlobRef, error) {
	// Sniff the first 1KB of it and don't print the stats if it looks like it was just a schema
	// blob.  We won't update the progress bar for that yet.
	var buf [1024]byte
	contents := buf[:0]
	sb, err := asr.sr.ReceiveBlob(blob, io.TeeReader(source, writeUntilSliceFull{&contents}))
	if err == nil && !schema.LikelySchemaBlob(contents) {
		asr.noteChunkOnServer(sb)
	}
	return sb, err
}

func (asr androidStatusRecevier) StatBlobs(dest chan<- blobref.SizedBlobRef, blobs []*blobref.BlobRef, wait time.Duration) error {
	midc := make(chan blobref.SizedBlobRef)
	errc := make(chan error, 1)
	go func() {
		err := asr.sr.StatBlobs(midc, blobs, wait)
		errc <- err
		close(midc)
	}()
	for sb := range midc {
		asr.noteChunkOnServer(sb)
		dest <- sb
	}
	return <-errc
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
	s = s[0 : l+growBy]
	copy(s[l:], p)
	*w.s = s
	return len(p), nil
}

// TODO: show in the Android UI stats about:
// * TCP connects/disconnects.
// * TCP bytes written.
// * TCP bytes received.
// * HTTP requests started.
// * HTTP requests sent.
// * HTTP headers received.
// * HTTP bodies received.
// * Byte up/down counts and summary of each HTTP request?
// For example:
//
// In-flight:
//   (17) blob-upload (123123423 up)
//
// Recently-completed: (last 10 or something)
//   (2) blob-upload (1 blobs) (483234 up / 1239 down, 200 OK)
//   (3) blob-stat (2 blobs) (483 up / 1239 down, 204 No Content)
//   (1) blob-stat (1 blobs) (483 up / 1239 down)
