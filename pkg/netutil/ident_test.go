/*
Copyright 2011 Google Inc.

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

package netutil

import (
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

var _ = log.Printf

func TestLocalIPv4(t *testing.T) {
	// Start listening on localhost IPv4, on some port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	testLocalListener(t, ln)
}

func TestLocalIPv6(t *testing.T) {
	ln, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Logf("skipping IPv6 test; not supported on host machine?")
		return
	}
	testLocalListener(t, ln)
}

func testLocalListener(t *testing.T, ln net.Listener) {
	defer ln.Close()

	// Accept a connection, run ConnUserId (what we're testing), and
	// send its result on c.
	type uidErr struct {
		uid int
		err error
	}
	c := make(chan uidErr, 2)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			c <- uidErr{0, err}
		}
		uid, err := ConnUserid(conn)
		c <- uidErr{uid, err}
	}()

	// Connect to our dummy server. Keep the connection open until
	// the test is done.
	donec := make(chan bool)
	defer close(donec)
	go func() {
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			return
		}
		<-donec
		c.Close()
	}()

	select {
	case r := <-c:
		if r.err != nil {
			t.Fatal(r.err)
		}
		if r.uid != os.Getuid() {
			t.Errorf("got uid %d; want %d", r.uid, os.Getuid())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
}

func TestHTTPAuth(t *testing.T) {
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		from, err := HostPortToIP(r.RemoteAddr)
		if err != nil {
			t.Fatal(err)
		}
		to := ts.Listener.Addr()
		uid, err := AddrPairUserid(from, to)
		if err != nil {
			fmt.Fprintf(rw, "ERR: %v", err)
			return
		}
		fmt.Fprintf(rw, "uid=%d", uid)
	}))
	defer ts.Close()
	res, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if g, e := string(body), fmt.Sprintf("uid=%d", os.Getuid()); g != e {
		t.Errorf("got body %q; want %q", g, e)
	}
}

func TestParseLinuxTCPStat4(t *testing.T) {
	lip, lport := net.ParseIP("67.218.110.129"), 43436
	rip, rport := net.ParseIP("207.7.148.195"), 80

	// 816EDA43:A9AC C39407CF:0050
	//          43436         80
	uid, err := uidFromReader(lip, lport, rip, rport, strings.NewReader(tcpstat4))
	if err != nil {
		t.Error(err)
	}
	if e, g := 61652, uid; e != g {
		t.Errorf("expected uid %d, got %d", e, g)
	}
}

var tcpstat4 = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode                                                     
0: 0100007F:C204 00000000:0000 0A 00000000:00000000 00:00000000 00000000 61652        0 8722922 1 ffff880036b36180 300 0 0 2 -1                   
1: 0100007F:0CEA 00000000:0000 0A 00000000:00000000 00:00000000 00000000   120        0 5714729 1 ffff880036b35480 300 0 0 2 -1                   
2: 0100007F:2BCB 00000000:0000 0A 00000000:00000000 00:00000000 00000000 65534        0 7381 1 ffff880136370000 300 0 0 2 -1                      
3: 0100007F:13AD 00000000:0000 0A 00000000:00000000 00:00000000 00000000 61652        0 4846349 1 ffff880123eb5480 300 0 0 2 -1                   
4: 00000000:0050 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 8307 1 ffff880123eb0d00 300 0 0 2 -1                      
5: 00000000:0071 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 8558503 1 ffff88001a242080 300 0 0 2 -1                   6: 0100007F:7533 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 8686 1 ffff880136371380 300 0 0 2 -1                      
7: 017AA8C0:0035 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 6015 1 ffff880123eb0680 300 0 0 2 -1                      
8: 0100007F:0277 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 8705543 1 ffff88001a242d80 300 0 0 2 -1                   
9: 816EDA43:D4DC 35E07D4A:01BB 01 00000000:00000000 02:00000E25 00000000 61652        0 8720744 2 ffff88001a243a80 346 4 24 3 2                   
10: 0100007F:C204 0100007F:D981 01 00000000:00000000 00:00000000 00000000 61652        0 8722934 1 ffff88006712a700 21 4 30 5 -1                   
11: 816EDA43:A9AC C39407CF:0050 01 00000000:00000000 00:00000000 00000000 61652        0 8754873 1 ffff88006712db00 27 0 0 3 -1                    
12: 816EDA43:AFEF 51357D4A:01BB 01 00000000:00000000 02:00000685 00000000 61652        0 8752937 2 ffff880136375480 87 4 2 4 -1                    
13: 0100007F:D981 0100007F:C204 01 00000000:00000000 00:00000000 00000000 61652        0 8722933 1 ffff880036b30d00 21 4 0 3 -1                    
`
