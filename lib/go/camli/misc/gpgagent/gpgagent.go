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

package gpgagent

import (
	"bufio"
	"fmt"
	"net"
	"io"
	"strings"
	"os"
)

type PassphraseRequest struct {
	CacheKey, ErrorMessage, Prompt, Desc string

	// Open file/socket to use, mostly for testing.
	// If nil, os.Getenv("GPG_AGENT_INFO") will be used.
	Conn io.ReadWriter
}

var ErrNoAgent = os.NewError("GPG_AGENT_INFO not set in environment")

func (pr *PassphraseRequest) GetPassphrase() (passphrase string, outerr os.Error) {
	defer func() {
		if e, ok := recover().(string); ok {
			passphrase = ""
			outerr = os.NewError(e)
		}
	}()

	conn := pr.Conn
	var br *bufio.Reader
	if conn == nil {
		sp := strings.Split(os.Getenv("GPG_AGENT_INFO"), ":", 3)
		if len(sp) == 0 || len(sp[0]) == 0 {
			return "", ErrNoAgent
		}
		var err os.Error
		addr := &net.UnixAddr{Net: "unix", Name: sp[0]}
		uc, err := net.DialUnix("unix", nil, addr)
		if err != nil {
			return "", err
		}
		defer uc.Close()
		br = bufio.NewReader(uc)
		lineb, err := br.ReadSlice('\n')
		if err != nil {
			return "", err
		}
		line := string(lineb)
		if !strings.HasPrefix(line, "OK") {
			return "", fmt.Errorf("didn't get OK; got %q", line)
		}
		conn = uc
	} else {
		br = bufio.NewReader(conn)
	}
	set := func(cmd string, val string) {
		if val == "" {
			return
		}
		fmt.Fprintf(conn, "%s %s\n", cmd, val)
		line, _, err := br.ReadLine()
		if err != nil {
			panic("Failed to " + cmd)
		}
		if !strings.HasPrefix(string(line), "OK") {
			panic("Response to " + cmd + " was " + string(line))
		}
	}
	if d := os.Getenv("DISPLAY"); d != "" {
		set("OPTION", "display="+d)
	}
	tty, err := os.Readlink("/proc/self/fd/0")
	if err == nil {
		set("OPTION", "ttyname="+tty)
	}
	set("OPTION", "ttytype="+os.Getenv("TERM"))
	fmt.Fprintf(conn, "GET_PASSPHRASE foo err+msg prompt desc\n")
	lineb, err := br.ReadSlice('\n')
	if err != nil {
		return "", err
	}
	line := string(lineb)

	return line, nil
}
