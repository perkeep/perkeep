/*
Copyright 2012 Google Inc.

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

package serverconfig_test

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/serverconfig"
)

func sortedKeys(m map[string]interface{}) (keys []string) {
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return
}

func prettyPrint(w io.Writer, i interface{}, indent int) {
	switch ei := i.(type) {
	case jsonconfig.Obj:
		for _, k := range sortedKeys(map[string]interface{}(ei)) {
			fmt.Fprintf(w, "\n")
			fmt.Fprintf(w, "%s: ", k)
			prettyPrint(w, ei[k], indent+1)
		}
		fmt.Fprintf(w, "\n")
	case map[string]interface{}:
		for _, k := range sortedKeys(ei) {
			fmt.Fprintf(w, "\n")
			for i := 0; i < indent; i++ {
				fmt.Fprintf(w, "	")
			}
			fmt.Fprintf(w, "%s: ", k)
			prettyPrint(w, ei[k], indent+1)
		}
		fmt.Fprintf(w, "\n")
	case []interface{}:
		fmt.Fprintf(w, "	")
		for _, v := range ei {
			prettyPrint(w, v, indent+1)
		}
	default:
		fmt.Fprintf(w, "%v, ", i)
	}
}

func TestConfigs(t *testing.T) {
	dir, err := os.Open("testdata")
	if err != nil {
		t.Fatal(err)
	}
	names, err := dir.Readdirnames(-1)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if strings.HasSuffix(name, ".json") {
			if strings.HasSuffix(name, "-want.json") {
				continue
			}
			testConfig(filepath.Join("testdata", name), t)
		}
	}
}

type namedReadSeeker struct {
	name string
	io.ReadSeeker
}

func (n namedReadSeeker) Name() string { return n.name }
func (n namedReadSeeker) Close() error { return nil }

func configParser() *jsonconfig.ConfigParser {
	// Make a custom jsonconfig ConfigParser whose reader rewrites "/path/to/secring" to the absolute
	// path of the jsonconfig test-secring.gpg file.
	secRing, err := filepath.Abs("../jsonsign/testdata/test-secring.gpg")
	if err != nil {
		panic(err)
	}
	return &jsonconfig.ConfigParser{
		Open: func(path string) (jsonconfig.File, error) {
			slurpBytes, err := ioutil.ReadFile(path)
			if err != nil {
				return nil, err
			}
			slurp := strings.Replace(string(slurpBytes), "/path/to/secring", secRing, 1)
			return namedReadSeeker{path, strings.NewReader(slurp)}, nil
		},
	}
}

func testConfig(name string, t *testing.T) {
	obj, err := configParser().ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	lowLevelConf, err := serverconfig.GenLowLevelConfig(&serverconfig.Config{jsonconfig.Obj: obj})
	if err != nil {
		t.Fatal(err)
	}
	wantFile := strings.Replace(name, ".json", "-want.json", 1)
	wantConf, err := configParser().ReadFile(wantFile)
	if err != nil {
		t.Fatal(err)
	}
	var got, want bytes.Buffer
	prettyPrint(&got, lowLevelConf.Obj, 0)
	prettyPrint(&want, wantConf, 0)
	if got.String() != want.String() {
		tempGot := tempFile(got.Bytes())
		tempWant := tempFile(want.Bytes())
		defer os.Remove(tempGot.Name())
		defer os.Remove(tempWant.Name())
		diff, err := exec.Command("diff", "-u", tempWant.Name(), tempGot.Name()).Output()
		if err != nil {
			t.Logf("diff failure: %v", err)
		}
		t.Errorf("Configurations differ.\nGot:\n%s\nWant:\n%s\nDiff:\n%s",
			&got, &want, diff)
	}
}

func tempFile(b []byte) *os.File {
	f, err := ioutil.TempFile("", "")
	if err != nil {
		panic(err)
	}
	_, err = f.Write(b)
	if err != nil {
		panic(err)
	}
	f.Close()
	return f
}
