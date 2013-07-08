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

// Package pinentry interfaces with the pinentry(1) command to securely
// prompt the user for a password using whichever user interface the
// user is currently using.
package pinentry

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ErrCancel is returned when the user explicitly aborts the password
// request.
var ErrCancel = errors.New("pinentry: Cancel")

// Request describes what the user should see during the request for
// their password.
type Request struct {
	Desc, Prompt, OK, Cancel, Error string
}

func catch(err *error) {
	rerr := recover()
	if rerr == nil {
		return
	}
	if e, ok := rerr.(string); ok {
		*err = errors.New(e)
	}
	if e, ok := rerr.(error); ok {
		*err = e
	}
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func (r *Request) GetPIN() (pin string, outerr error) {
	defer catch(&outerr)
	bin, err := exec.LookPath("pinentry")
	if err != nil {
		return r.getPINNaïve()
	}
	cmd := exec.Command(bin)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	check(cmd.Start())
	defer cmd.Wait()
	defer stdin.Close()
	br := bufio.NewReader(stdout)
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
		fmt.Fprintf(stdin, "%s %s\n", cmd, val)
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
	set("OPTION", "ttytype="+os.Getenv("TERM"))
	tty, err := os.Readlink("/proc/self/fd/0")
	if err == nil {
		set("OPTION", "ttyname="+tty)
	}
	fmt.Fprintf(stdin, "GETPIN\n")
	lineb, _, err = br.ReadLine()
	if err != nil {
		return "", fmt.Errorf("Failed to read line after GETPIN: %v", err)
	}
	line = string(lineb)
	if strings.HasPrefix(line, "D ") {
		return line[2:], nil
	}
	if strings.HasPrefix(line, "ERR 83886179 ") {
		return "", ErrCancel
	}
	return "", fmt.Errorf("GETPIN response didn't start with D; got %q", line)
}

func runPass(bin string, args ...string) {
	cmd := exec.Command(bin, args...)
	cmd.Stdout = os.Stdout
	cmd.Run()
}

func (r *Request) getPINNaïve() (string, error) {
	stty, err := exec.LookPath("stty")
	if err != nil {
		return "", errors.New("no pinentry or stty found")
	}
	runPass(stty, "-echo")
	defer runPass(stty, "echo")

	if r.Desc != "" {
		fmt.Printf("%s\n\n", r.Desc)
	}
	prompt := r.Prompt
	if prompt == "" {
		prompt = "Password"
	}
	fmt.Printf("%s: ", prompt)
	br := bufio.NewReader(os.Stdin)
	line, _, err := br.ReadLine()
	if err != nil {
		return "", err
	}
	return string(line), nil
}
