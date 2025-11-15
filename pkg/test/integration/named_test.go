/*
Copyright 2014 The Perkeep Authors

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

package integration

import (
	"bufio"
	"encoding/json"
	"strings"
	"testing"

	"perkeep.org/pkg/test"
)

func runCmd(t *testing.T, w *test.World, cmd string, args ...string) string {
	out, err := test.RunCmd(w.Cmd(cmd, args...))
	if err != nil {
		t.Fatalf("Error running cmd %q %q: %v\n", cmd, args, err)
	}
	return out
}

func parseJSON(s string) map[string]any {
	m := make(map[string]any)
	err := json.Unmarshal([]byte(s), &m)
	if err != nil {
		panic(err)
	}
	return m
}

func TestSetNamed(t *testing.T) {
	w := test.GetWorld(t)
	// Needed to upload the owner public key
	runCmd(t, w, "pk-put", "permanode")

	runCmd(t, w, "pk", "named-search-set", "bar", "is:image and tag:bar")
	gno := runCmd(t, w, "pk", "named-search-get", "bar")
	gnr := parseJSON(gno)
	if gnr["named"] != "bar" || gnr["substitute"] != "is:image and tag:bar" {
		t.Errorf("Unexpected value %v , expected (bar, is:image and tag:bar)", gnr)
	}
}

func TestGetNamed(t *testing.T) {
	w := test.GetWorld(t)

	putExprCmd := w.Cmd("pk-put", "blob", "-")
	putExprCmd.Stdin = strings.NewReader("is:pano")
	ref, err := test.RunCmd(putExprCmd)
	if err != nil {
		t.Fatal(err)
	}

	pn := runCmd(t, w, "pk-put", "permanode")
	runCmd(t, w, "pk-put", "attr", strings.TrimSpace(pn), "camliNamedSearch", "foo")
	runCmd(t, w, "pk-put", "attr", strings.TrimSpace(pn), "camliContent", strings.TrimSpace(ref))
	gno := runCmd(t, w, "pk", "named-search-get", "foo")
	gnr := parseJSON(gno)
	if gnr["named"] != "foo" || gnr["substitute"] != "is:pano" {
		t.Errorf("Unexpected value %v , expected (foo, is:pano)", gnr)
	}
}

func TestNamedSearch(t *testing.T) {
	w := test.GetWorld(t)

	runCmd(t, w, "pk", "named-search-set", "favorite", "tag:cats")
	pn := runCmd(t, w, "pk-put", "permanode", "-title", "Felix", "-tag", "cats")
	_, lines, err := bufio.ScanLines([]byte(pn), false)
	if err != nil {
		t.Fatal(err)
	}
	pn = string(lines[0])

	sr := runCmd(t, w, "pk", "search", "named:favorite")
	if !strings.Contains(sr, pn) {
		t.Fatalf("Expected %v in %v", pn, sr)
	}
}

func TestNestedNamedSearch(t *testing.T) {
	w := test.GetWorld(t)

	runCmd(t, w, "pk", "named-search-set", "favorite", "tag:cats")
	runCmd(t, w, "pk", "named-search-set", "mybest", "named:favorite")
	pn := runCmd(t, w, "pk-put", "permanode", "-title", "Felix", "-tag", "cats")
	_, lines, err := bufio.ScanLines([]byte(pn), false)
	if err != nil {
		t.Fatal(err)
	}
	pn = string(lines[0])

	sr := runCmd(t, w, "pk", "search", "named:mybest")
	if !strings.Contains(sr, pn) {
		t.Fatalf("Expected %v in %v", pn, sr)
	}
}
