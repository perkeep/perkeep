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

package pinentry

import (
	"bufio"
	"exec"
	"fmt"
	"log"
	"os"
	"strings"
)

var _ = log.Printf

type Request struct {
	Desc, Prompt, OK, Cancel, Error string
}

func (r *Request) GetPIN() (pin string, outerr os.Error) {
	defer func() {
		if e, ok := recover().(string); ok {
			pin = ""
			outerr = os.NewError(e)
		}
	}()
	c, err := exec.Run("/usr/bin/pinentry",
		[]string{"/usr/bin/pinentry"},
		os.Environ(),
		"/",
		exec.Pipe,
		exec.Pipe,
		exec.DevNull)
	if err != nil {
		return "", err
	}
	defer func() {
		c.Stdin.Close()
		c.Stdout.Close()
		c.Close()
		c.Wait(0)
	}()
	br := bufio.NewReader(c.Stdout)
	lineb, _, err := br.ReadLine()
	if err != nil {
		return "", fmt.Errorf("Failed to get getpin greeting")
	}
	line := string(lineb)
	if !strings.HasPrefix(line, "OK") {
		return "", fmt.Errorf("getpin greeting said %q", line)
	}
	set := func(cmd string, val string) {
		if val == "" {
			return
		}
		fmt.Fprintf(c.Stdin, "%s %s\n", cmd, val)
		line, _, err := br.ReadLine()
		if err != nil {
			panic("Failed to " + cmd)
		}
		if string(line) != "OK" {
			panic("Response to " + cmd + " was " + string(line))
		}
	}
	set("SETPROMPT", r.Prompt)
	set("SETDESC", r.Desc)
	set("SETOK", r.OK)
	set("SETCANCEL", r.Cancel)
	set("SETERROR", r.Error)
	set("OPTION", "ttytype=" + os.Getenv("TERM"))
	tty, err := os.Readlink("/proc/self/fd/0")
	if err == nil {
		set("OPTION", "ttyname=" + tty)
	}
	fmt.Fprintf(c.Stdin, "GETPIN\n")
	lineb, _, err = br.ReadLine()
	if err != nil {
		return "", fmt.Errorf("Failed to read line after GETPIN: %v", err)
	}
	line = string(lineb)
	if !strings.HasPrefix(line, "D ") {
		return "", os.NewError("GETPIN response didn't start with D")
	}
	return line[2:], nil
}
