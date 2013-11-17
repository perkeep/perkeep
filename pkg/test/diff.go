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
	"bytes"
	"io/ioutil"
	"os"
	"os/exec"
)

// Diff returns the unified diff (from running "diff -u") or
// returns an error string.
func Diff(a, b []byte) string {
	if bytes.Equal(a, b) {
		return ""
	}
	ta, err := ioutil.TempFile("", "")
	if err != nil {
		return err.Error()
	}
	tb, err := ioutil.TempFile("", "")
	if err != nil {
		return err.Error()
	}
	defer os.Remove(ta.Name())
	defer os.Remove(tb.Name())
	// Lqzy...
	ta.Write(a)
	tb.Write(b)
	ta.Close()
	tb.Close()
	out, err := exec.Command("diff", "-u", ta.Name(), tb.Name()).CombinedOutput()
	if err != nil && len(out) == 0 {
		return err.Error()
	}
	return string(out)
}
