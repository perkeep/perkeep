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

package test

import (
	"log"
	"os"
	"strconv"
	"strings"
	"testing"
)

// BrokenTest marks the test as broken and calls t.Skip, unless the environment
// variable RUN_BROKEN_TESTS is set to 1 (or some other boolean true value).
func BrokenTest(t *testing.T) {
	if v, _ := strconv.ParseBool(os.Getenv("RUN_BROKEN_TESTS")); !v {
		t.Skipf("Skipping broken tests without RUN_BROKEN_TESTS=1")
	}
}

// TLog changes the log package's output to log to t and returns a function
// to reset it back to stderr.
func TLog(t testing.TB) func() {
	log.SetOutput(twriter{t: t})
	return func() {
		log.SetOutput(os.Stderr)
	}
}

type twriter struct {
	t            testing.TB
	quietPhrases []string
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
	if w.t != nil {
		w.t.Log(strings.TrimSuffix(string(p), "\n"))
	}
	return len(p), nil
}

// NewLogger returns a logger that logs to t with the given prefix.
//
// The optional quietPhrases are substrings to match in writes to
// determine whether those log messages are muted.
func NewLogger(t *testing.T, prefix string, quietPhrases ...string) *log.Logger {
	return log.New(twriter{t: t, quietPhrases: quietPhrases}, prefix, log.LstdFlags)
}
