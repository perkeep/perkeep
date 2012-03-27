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
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strconv"
	"strings"
)

// ConnUserid returns the uid that owns the given localhost connection.
// The returned error is os.ErrNotExist if the connection wasn't found.
func ConnUserid(conn net.Conn) (uid int, err error) {
	return AddrPairUserid(conn.LocalAddr().String(), conn.RemoteAddr().String())
}

func splitIPPort(param, value string) (ip net.IP, port int, reterr error) {
	addrs, ports, err := net.SplitHostPort(value)
	if err != nil {
		reterr = fmt.Errorf("netutil: AddrPairUserid invalid %s value of %q: %v", param, value, err)
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
// respectively).  Returns os.ErrNotExist for the error if the TCP connection
// isn't found.
func AddrPairUserid(lipport, ripport string) (uid int, err error) {
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

	if runtime.GOOS == "darwin" {
		return uidFromDarwinLsof(lip, lport, rip, rport)
	}

	file := "/proc/net/tcp"
	if !localv4 {
		file = "/proc/net/tcp6"
	}
	f, err := os.Open(file)
	if err != nil {
		return -1, fmt.Errorf("Error opening %s: %v", file, err)
	}
	defer f.Close()
	return uidFromReader(lip, lport, rip, rport, f)
}

func reverseIPBytes(b []byte) []byte {
	rb := make([]byte, len(b))
	for i, v := range b {
		rb[len(b)-i-1] = v
	}
	return rb
}

func uidFromDarwinLsof(lip net.IP, lport int, rip net.IP, rport int) (uid int, err error) {
	seek := fmt.Sprintf("%s:%d->%s:%d", lip, lport, rip, rport)
	seekb := []byte(seek)

	cmd := exec.Command("lsof", "-a", "-n", "-i", "-P")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return
	}
	defer cmd.Wait()
	defer stdout.Close()
	err = cmd.Start()
	if err != nil {
		return
	}
	defer cmd.Process.Kill()
	br := bufio.NewReader(stdout)
	for {
		line, err := br.ReadSlice('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			return -1, err
		}
		if !bytes.Contains(line, seekb) {
			continue
		}
		// SystemUIS   276 bradfitz   15u  IPv4 0xffffff801a7c74e0      0t0  TCP 127.0.0.1:56718->127.0.0.1:5204 (ESTABLISHED)
		f := bytes.Fields(line)
		if len(f) < 8 {
			continue
		}
		username := string(f[2])
		if uid := os.Getuid(); uid != 0 && username == os.Getenv("USER") {
			return uid, nil
		}
		u, err := user.Lookup(username)
		if err == nil {
			uid, err := strconv.Atoi(u.Uid)
			return uid, err
		}
		return 0, err
	}
	return -1, os.ErrNotExist

}

func uidFromReader(lip net.IP, lport int, rip net.IP, rport int, r io.Reader) (uid int, err error) {
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
			return -1, os.ErrNotExist
		}
		parts := strings.Fields(strings.TrimSpace(line))
		if len(parts) < 8 {
			continue
		}
		// log.Printf("parts[1] = %q; localHex = %q", parts[1], localHex)
		if parts[1] == localHex && parts[2] == remoteHex {
			uid, err = strconv.Atoi(parts[7])
			return uid, err
		}
	}
	panic("unreachable")
}
