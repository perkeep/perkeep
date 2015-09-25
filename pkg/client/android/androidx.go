/*
Copyright 2013 The Camlistore Authors

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

// Package android contains code specific to running the Camlistore client
// code as a child process on Android. This removes ugly API from the
// client package itself.
package android

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/osutil"
	"camlistore.org/pkg/schema"
)

// TODO(mpl): distinguish CAMPUT, CAMGET, etc
var androidOutput, _ = strconv.ParseBool(os.Getenv("CAMPUT_ANDROID_OUTPUT"))

func PreExit() {
	if !IsChild() {
		return
	}
	Printf("STAT exit 1\n")
}

var androidOutMu sync.Mutex

func Printf(format string, args ...interface{}) {
	androidOutMu.Lock()
	defer androidOutMu.Unlock()
	fmt.Printf(format, args...)
}

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
	Printf("STAT %s %d\n", ni.name, nv)
}

func (ni *namedInt) Set(v int64) {
	ni.Lock()
	if v == ni.val {
		ni.Unlock()
		return
	}
	ni.val = v
	ni.Unlock()
	Printf("STAT %s %d\n", ni.name, v)
}

var (
	statDNSStart       = &namedInt{name: "dns_start"}
	statDNSDone        = &namedInt{name: "dns_done"}
	statTCPStart       = &namedInt{name: "tcp_start"}
	statTCPStarted     = &namedInt{name: "tcp_started"}
	statTCPFail        = &namedInt{name: "tcp_fail"}
	statTCPDone        = &namedInt{name: "tcp_done_because_close"}
	statTCPDoneRead    = &namedInt{name: "tcp_done_because_read"}
	statTCPWrites      = &namedInt{name: "tcp_write_byte"}
	statTCPWrote       = &namedInt{name: "tcp_wrote_byte"}
	statTCPReads       = &namedInt{name: "tcp_read_byte"}
	statHTTPStart      = &namedInt{name: "http_start"}
	statHTTPResHeaders = &namedInt{name: "http_res_headers"}
	statBlobUploaded   = &namedInt{name: "blob_uploaded"}
	statBlobExisted    = &namedInt{name: "blob_existed"}
	statFileUploaded   = &namedInt{name: "file_uploaded"}
	statFileExisted    = &namedInt{name: "file_existed"}
	statMemReleased    = &namedInt{name: "mem_heap_released"}
	statMemAlloc       = &namedInt{name: "mem_alloc"}
	statMemRSS         = &namedInt{name: "mem_rss"}
)

type statTrackingConn struct {
	net.Conn
	once sync.Once // guards close stats
}

func (c *statTrackingConn) Write(p []byte) (n int, err error) {
	statTCPWrites.Incr(int64(len(p)))
	n, err = c.Conn.Write(p)
	statTCPWrote.Incr(int64(n))
	return
}

func (c *statTrackingConn) Read(p []byte) (n int, err error) {
	n, err = c.Conn.Read(p)
	statTCPReads.Incr(int64(n))
	if err != nil {
		c.once.Do(func() {
			statTCPDoneRead.Incr(1)
		})
	}
	return
}

func (c *statTrackingConn) Close() error {
	c.once.Do(func() {
		statTCPDone.Incr(1)
	})
	return nil
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
			log.Printf("Failed to resolve %q with ping", host)
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
		log.Printf("Failed to resolve %q with ping", host)
		c <- host
	}()
	return <-c
	return v
}

type StatsTransport struct {
	Rt http.RoundTripper
}

func (t StatsTransport) RoundTrip(req *http.Request) (res *http.Response, err error) {
	statHTTPStart.Incr(1)
	res, err = t.Rt.RoundTrip(req)
	statHTTPResHeaders.Incr(1)
	// TODO: track per-response code stats, and also track when body done being read.
	return
}

func Dial(network, addr string) (net.Conn, error) {
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
	if OnAndroid() {
		// Only do the Android DNS lookups when actually
		// running on a device. We also run in "Android mode"
		// (IsChild) in tests and interactive debugging.
		host = androidLookupHost(host)
	}
	c, err := net.Dial(network, net.JoinHostPort(host, port))
	if err != nil {
		statTCPFail.Incr(1)
		return nil, err
	}
	statTCPStarted.Incr(1)
	return &statTrackingConn{Conn: c}, err
}

func TLSConfig() (*tls.Config, error) {
	if !OnAndroid() {
		return nil, nil
	}
	certDir := "/system/etc/security/cacerts"
	fi, err := os.Stat(certDir)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("%q not a dir", certDir)
	}
	pool := x509.NewCertPool()
	cfg := &tls.Config{RootCAs: pool}

	f, err := os.Open(certDir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	names, _ := f.Readdirnames(-1)
	for _, name := range names {
		pem, err := ioutil.ReadFile(filepath.Join(certDir, name))
		if err != nil {
			return nil, err
		}
		pool.AppendCertsFromPEM(pem)
	}
	return cfg, nil
}

// NoteFileUploaded is a hook for camput to report that a file
// was uploaded.  TODO: move this to pkg/client/android probably.
func NoteFileUploaded(fullPath string, uploaded bool) {
	if !IsChild() {
		return
	}
	if uploaded {
		statFileUploaded.Incr(1)
	} else {
		statFileExisted.Incr(1)
	}
	Printf("FILE_UPLOADED %s\n", fullPath)
}

// androidStatusReceiver is a blobserver.StatReceiver wrapper that
// reports the full filename path and size of uploaded blobs.
// The android app wrapping camput watches stdout for this, for progress bars.
type StatusReceiver struct {
	Sr   blobserver.StatReceiver
	Path string
}

func (asr StatusReceiver) noteChunkOnServer(sb blob.SizedRef) {
	Printf("CHUNK_UPLOADED %d %s %s\n", sb.Size, sb.Ref, asr.Path)
}

func (asr StatusReceiver) ReceiveBlob(blob blob.Ref, source io.Reader) (blob.SizedRef, error) {
	// Sniff the first 1KB of it and don't print the stats if it looks like it was just a schema
	// blob.  We won't update the progress bar for that yet.
	var buf [1024]byte
	contents := buf[:0]
	sb, err := asr.Sr.ReceiveBlob(blob, io.TeeReader(source, writeUntilSliceFull{&contents}))
	if err == nil && !schema.LikelySchemaBlob(contents) {
		statBlobUploaded.Incr(1)
		asr.noteChunkOnServer(sb)
	}
	return sb, err
}

func (asr StatusReceiver) StatBlobs(dest chan<- blob.SizedRef, blobs []blob.Ref) error {
	midc := make(chan blob.SizedRef)
	errc := make(chan error, 1)
	go func() {
		err := asr.Sr.StatBlobs(midc, blobs)
		errc <- err
		close(midc)
	}()
	for sb := range midc {
		asr.noteChunkOnServer(sb)
		statBlobExisted.Incr(1)
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

var memOnce sync.Once

func startMemGoroutine() {
	if !androidOutput {
		return
	}
	go func() {
		var ms runtime.MemStats
		n := 0
		for {
			runtime.ReadMemStats(&ms)
			statMemReleased.Set(int64(ms.HeapReleased))
			statMemAlloc.Set(int64(ms.Alloc))
			statMemRSS.Set(osutil.MemUsage())
			time.Sleep(1 * time.Second)
			n++
			if n%5 == 0 {
				debug.FreeOSMemory()
			}
		}
	}()
}
