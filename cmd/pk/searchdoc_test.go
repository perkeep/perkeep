/*
Copyright 2026 The Perkeep Authors

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

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"perkeep.org/pkg/cmdmain"
)

func TestSearchDoc(t *testing.T) {
	golden, err := os.ReadFile("testdata/searchdoc.json")
	if err != nil {
		t.Fatal(err)
	}
	// Quoted lines preserve the legacy tabs, trailing spaces and final newline
	// without requiring trailing whitespace in the golden file itself.
	var lines []string
	if err := json.Unmarshal(golden, &lines); err != nil {
		t.Fatal(err)
	}
	want := []byte(strings.Join(lines, "\n"))
	var out bytes.Buffer
	// These tests cannot run in parallel while replacing command output.
	original := cmdmain.Stdout
	cmdmain.Stdout = &out
	t.Cleanup(func() { cmdmain.Stdout = original })
	cmd := new(searchDocCmd)
	if err := cmd.RunCommand(nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), want) {
		t.Errorf("searchdoc output changed:\ngot:\n%s\nwant:\n%s", out.Bytes(), want)
	}
	out.Reset()
	if err := cmd.RunCommand([]string{"unexpected"}); err == nil {
		t.Error("searchdoc accepted an argument")
	}
	if out.Len() != 0 {
		t.Error("searchdoc printed help after rejecting arguments")
	}
}
