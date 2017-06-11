// Copyright (c) 2016 Paul Jolly <paul@myitcv.org.uk>, all rights reserved.
// Use of this document is governed by a license found in the LICENSE document.

package gogenerate

import (
	"bufio"
	"bytes"
	"fmt"
	"go/build"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The following code is largely adapted from cmd/go. We reproduce the license
// here therefore

// Copyright (c) 2009 The Go Authors. All rights reserved.

// Redistribution and use in source and binary forms, with or without
// modification, are permitted provided that the following conditions are
// met:

//    * Redistributions of source code must retain the above copyright
// notice, this list of conditions and the following disclaimer.
//    * Redistributions in binary form must reproduce the above
// copyright notice, this list of conditions and the following disclaimer
// in the documentation and/or other materials provided with the
// distribution.
//    * Neither the name of Google Inc. nor the names of its
// contributors may be used to endorse or promote products derived from
// this software without specific prior written permission.

// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
// "AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
// LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
// A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
// OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
// SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
// LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
// DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
// THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
// (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
// OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

// A generator represents the state of a single Go source file
// being scanned for generator commands.
type generator struct {
	f        func(line int, dirArgs []string) error
	r        io.Reader
	path     string // full rooted path name.
	dir      string // full rooted directory of file.
	file     string // base name of file.
	pkg      string
	commands map[string][]string
	lineNum  int // current line number.
	env      []string
}

// DirFunc runs f(cmds) on each go generate directive (as defined by
// go generate -help) found in the absolute-named file that is part
// of package pkg
func DirFunc(pkg string, name string, f func(line int, dirArgs []string) error) error {
	fi, err := os.Open(name)
	if err != nil {
		return err
	}
	defer fi.Close()

	g := &generator{
		f:        f,
		pkg:      pkg,
		commands: make(map[string][]string),
		path:     name,
		r:        fi,
	}

	return g.matches()
}

// run runs the generators in the current file.
func (g *generator) matches() (err error) {
	// Processing below here calls g.errorf on failure, which does panic(stop).
	// If we encounter an error, we abort the package.
	defer func() {
		e := recover()
		if e, isErr := e.(error); isErr {
			err = e
		}
	}()

	g.dir, g.file = filepath.Split(g.path)
	g.dir = filepath.Clean(g.dir) // No final separator please.

	// Scan for lines that start "//go:generate".
	// Can't use bufio.Scanner because it can't handle long lines,
	// which are likely to appear when using generate.
	input := bufio.NewReader(g.r)
	// One line per loop.
	for {
		g.lineNum++ // 1-indexed.
		var buf []byte
		buf, err = input.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			// Line too long - consume and ignore.
			if isGoGenerate(buf) {
				g.errorf("directive too long")
			}
			for err == bufio.ErrBufferFull {
				_, err = input.ReadSlice('\n')
			}
			if err != nil {
				break
			}
			continue
		}

		if err != nil {
			// Check for marker at EOF without final \n.
			if err == io.EOF && isGoGenerate(buf) {
				err = io.ErrUnexpectedEOF
			}
			break
		}

		if !isGoGenerate(buf) {
			continue
		}

		g.setEnv()
		words := g.split(string(buf))
		if len(words) == 0 {
			g.errorf("no arguments to directive")
		}
		if words[0] == "-command" {
			g.setShorthand(words)
			continue
		}

		err := g.f(g.lineNum, words)
		if err != nil {
			g.errorf("callback error: %v", err)
		}
	}
	if err != nil && err != io.EOF {
		g.errorf("error reading")
	}

	return nil
}

func isGoGenerate(buf []byte) bool {
	return bytes.HasPrefix(buf, []byte(GoGeneratePrefix+" ")) || bytes.HasPrefix(buf, []byte(GoGeneratePrefix+"\t"))
}

// setEnv sets the extra environment variables used when executing a
// single go:generate command.
func (g *generator) setEnv() {
	g.env = []string{
		"GOARCH=" + build.Default.GOARCH,
		"GOOS=" + build.Default.GOOS,
		"GOFILE=" + g.file,
		"GOLINE=" + strconv.Itoa(g.lineNum),
		"GOPACKAGE=" + g.pkg,
		"DOLLAR=" + "$",
	}
}

// split breaks the line into words, evaluating quoted
// strings and evaluating environment variables.
// The initial //go:generate element is present in line.
func (g *generator) split(line string) []string {
	// Parse line, obeying quoted strings.
	var words []string
	line = line[len("//go:generate ") : len(line)-1] // Drop preamble and final newline.
	// There may still be a carriage return.
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	// One (possibly quoted) word per iteration.
Words:
	for {
		line = strings.TrimLeft(line, " \t")
		if len(line) == 0 {
			break
		}
		if line[0] == '"' {
			for i := 1; i < len(line); i++ {
				c := line[i] // Only looking for ASCII so this is OK.
				switch c {
				case '\\':
					if i+1 == len(line) {
						g.errorf("bad backslash")
					}
					i++ // Absorb next byte (If it's a multibyte we'll get an error in Unquote).
				case '"':
					word, err := strconv.Unquote(line[0 : i+1])
					if err != nil {
						g.errorf("bad quoted string")
					}
					words = append(words, word)
					line = line[i+1:]
					// Check the next character is space or end of line.
					if len(line) > 0 && line[0] != ' ' && line[0] != '\t' {
						g.errorf("expect space after quoted argument")
					}
					continue Words
				}
			}
			g.errorf("mismatched quoted string")
		}
		i := strings.IndexAny(line, " \t")
		if i < 0 {
			i = len(line)
		}
		words = append(words, line[0:i])
		line = line[i:]
	}
	// Substitute command if required.
	if len(words) > 0 && g.commands[words[0]] != nil {
		// Replace 0th word by command substitution.
		words = append(g.commands[words[0]], words[1:]...)
	}
	// Substitute environment variables.
	for i, word := range words {
		words[i] = os.Expand(word, g.expandVar)
	}
	return words
}

// errorf logs an error message prefixed with the file and line number.
// It then exits the program (with exit status 1) because generation stops
// at the first error.
func (g *generator) errorf(format string, args ...interface{}) {
	panic(fmt.Errorf("%s:%d: %s", g.path, g.lineNum, fmt.Sprintf(format, args...)))
}

// expandVar expands the $XXX invocation in word. It is called
// by os.Expand.
func (g *generator) expandVar(word string) string {
	w := word + "="
	for _, e := range g.env {
		if strings.HasPrefix(e, w) {
			return e[len(w):]
		}
	}
	return os.Getenv(word)
}

// setShorthand installs a new shorthand as defined by a -command directive.
func (g *generator) setShorthand(words []string) {
	// Create command shorthand.
	if len(words) == 1 {
		g.errorf("no command specified for -command")
	}
	command := words[1]
	if g.commands[command] != nil {
		g.errorf("command %q defined multiply defined", command)
	}
	g.commands[command] = words[2:len(words):len(words)] // force later append to make copy
}
