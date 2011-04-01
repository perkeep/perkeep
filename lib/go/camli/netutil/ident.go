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
	"bufio"
	"fmt"
	"net"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
)

var _ = log.Printf

// TODO: Linux-specific right now.
// Returns os.ENOENT on not found.
func ConnUserid(conn net.Conn) (uid int, err os.Error) {
	return AddrPairUserid(conn.LocalAddr().String(), conn.RemoteAddr().String())
}

func splitIPPort(param, value string) (ip net.IP, port int, reterr os.Error) {
	addrs, ports, err := net.SplitHostPort(value)
	if err != nil {
		reterr = fmt.Errorf("netutil: AddrPairUserid invalid %s value: %v", err)
                return
        }
	ip = net.ParseIP(addrs)
	if ip == nil {
		reterr = fmt.Errorf("netutil: invalid %s IP %q", param, addrs)
		return
	}
	port, err = strconv.Atoi(ports)
	if err != nil || port <= 0 || port > 65535 {
		reterr = fmt.Errorf("netutil: invalid port %q", ports)
		return
	}
	return
}

// AddrPairUserid returns the local userid who owns the TCP connection
// given by the local and remote ip:port (lipport and ripport,
// respectively).  Returns os.ENOENT for the error if the TCP connection
// isn't found.
func AddrPairUserid(lipport, ripport string) (uid int, err os.Error) {
	lip, lport, err := splitIPPort("lipport", lipport)
	if err != nil {
		return -1, err
	}
	rip, rport, err := splitIPPort("ripport", ripport)
	if err != nil {
		return -1, err
	}
	localv4 := (lip.To4() != nil)
	remotev4 := (rip.To4() != nil)
	if localv4 != remotev4 {
		return -1, fmt.Errorf("netutil: address pairs of different families; localv4=%v, remotev4=%v",
			localv4, remotev4)
	}

	file := "/proc/net/tcp"
	if !localv4 {
		file = "/proc/net/tcp6"
	}
	f, err := os.Open(file, os.O_RDONLY, 0)
	if err != nil {
		return -1, fmt.Errorf("Error opening %s: %v", file, err)
	}
	defer f.Close()
	return uidFromReader(lip, lport, rip, rport, f)
}

func reverseIPBytes(b []byte) []byte {
	rb := make([]byte, len(b))
	for i, v := range b {
		rb[len(b) - i - 1] = v
	}
	return rb
}

func uidFromReader(lip net.IP, lport int, rip net.IP, rport int, r io.Reader) (uid int, err os.Error) {
	buf := bufio.NewReader(r)

	localHex := ""
	remoteHex := ""
	if lip.To4() != nil {
		// In the kernel, the port is run through ntohs(), and
		// the inet_request_socket in
		// include/net/inet_socket.h says the "loc_addr" and
		// "rmt_addr" fields are __be32, but get_openreq4's
		// printf of them is raw, without byte order
		// converstion.
		localHex = fmt.Sprintf("%08X:%04X", reverseIPBytes([]byte(lip.To4())), lport)
		remoteHex = fmt.Sprintf("%08X:%04X", reverseIPBytes([]byte(rip.To4())), rport)
	} else {
		localHex = fmt.Sprintf("%032X:%04X", []byte(lip.To16()), lport)
		remoteHex = fmt.Sprintf("%032X:%04X", []byte(rip.To16()), rport)
	}
	
	for {
		line, err := buf.ReadString('\n')
		if err != nil {
			return -1, os.ENOENT
		}
		parts := strings.Fields(strings.TrimSpace(line))
		if len(parts) < 8 {
			continue
		}
		// log.Printf("parts[1] = %q; localHex = %q", parts[1], localHex)
		if parts[1] == localHex && parts[2] == remoteHex {
			uid, _ = strconv.Atoi(parts[7])
			return
		}
	}
	panic("unreachable")
}


