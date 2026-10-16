/*
Copyright 2013 The Perkeep Authors

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

package test

import (
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ClockOrigin is an arbitrary contemporary date that can be used as a starting
// time in tests. It is 2011-11-28 01:32:36.000123456 +0000 UTC.
var ClockOrigin = time.Unix(1322443956, 123456)

// BrokenTest marks the test as broken and calls t.Skip, unless the environment
// variable RUN_BROKEN_TESTS is set to 1 (or some other boolean true value).
func BrokenTest(t *testing.T) {
	if v, _ := strconv.ParseBool(os.Getenv("RUN_BROKEN_TESTS")); !v {
		t.Skipf("Skipping broken tests without RUN_BROKEN_TESTS=1")
	}
}

// TLog changes the log package's output to log to t and registers a
// t.Cleanup function to reset it back to stderr.
func TLog(t testing.TB) {
	log.SetOutput(twriter{t: t})
	t.Cleanup(func() {
		log.SetOutput(os.Stderr)
	})
}

type twriter struct {
	t            testing.TB
	quietPhrases []string
	state        *twriterState // or nil to always log to t
}

// twriterState tracks whether a twriter's test has finished, after which
// calling t.Log would panic.
type twriterState struct {
	mu   sync.Mutex
	done bool
}

func (w twriter) Write(p []byte) (n int, err error) {
	if len(w.quietPhrases) > 0 {
		s := string(p)
		for _, phrase := range w.quietPhrases {
			if strings.Contains(s, phrase) {
				return len(p), nil
			}
		}
	}
	if w.t == nil {
		return len(p), nil
	}
	if w.state != nil {
		w.state.mu.Lock()
		defer w.state.mu.Unlock()
		if w.state.done {
			// A goroutine outlived its test; don't lose what it says.
			return os.Stderr.Write(p)
		}
	}
	w.t.Log(strings.TrimSuffix(string(p), "\n"))
	return len(p), nil
}

// NewLogger returns a logger that logs to t with the given prefix.
//
// The optional quietPhrases are substrings to match in writes to
// determine whether those log messages are muted.
//
// Writes after t's cleanup has run go to os.Stderr instead, since
// calling t.Log after a test completes panics.
func NewLogger(t testing.TB, prefix string, quietPhrases ...string) *log.Logger {
	state := new(twriterState)
	t.Cleanup(func() {
		state.mu.Lock()
		defer state.mu.Unlock()
		state.done = true
	})
	return log.New(twriter{t: t, quietPhrases: quietPhrases, state: state}, prefix, log.LstdFlags)
}
