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

package leak

import (
	"bytes"
	"fmt"
	"log"
	"runtime"
)

// A Checker checks for leaks.
type Checker struct {
	pc []uintptr // nil once closed
}

// NewChecker returns a Checker, remembering the stack trace.
func NewChecker() *Checker {
	pc := make([]uintptr, 50)
	ch := &Checker{pc[:runtime.Callers(0, pc)]}
	runtime.SetFinalizer(ch, (*Checker).finalize)
	return ch
}

func (c *Checker) Close() {
	if c != nil {
		c.pc = nil
	}
}

func (c *Checker) finalize() {
	if testHookFinalize != nil {
		defer testHookFinalize()
	}
	if c == nil || c.pc == nil {
		return
	}
	var buf bytes.Buffer
	buf.WriteString("Leak at:\n")
	for _, pc := range c.pc {
		f := runtime.FuncForPC(pc)
		if f == nil {
			break
		}
		file, line := f.FileLine(f.Entry())
		fmt.Fprintf(&buf, "  %s:%d\n", file, line)
	}
	onLeak(c, buf.String())
}

// testHookFinalize optionally specifies a function to run after
// finalization.  For tests.
var testHookFinalize func()

// onLeak is changed by tests.
var onLeak = logLeak

func logLeak(c *Checker, stack string) {
	log.Println(stack)
}
