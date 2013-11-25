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
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/serverconfig"
	"camlistore.org/pkg/test"
)

var updateGolden = flag.Bool("update_golden", false, "Update golden *.want files")

const secringPlaceholder = "/path/to/secring"

func init() {
	// Avoid Linux vs. OS X differences in tests.
	serverconfig.SetTempDirFunc(func() string { return "/tmp" })
	serverconfig.SetNoMkdir(true)
}

func sortedKeys(m map[string]interface{}) (keys []string) {
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return
}

func prettyPrint(t *testing.T, w io.Writer, i interface{}, indent int) {
	out, err := json.MarshalIndent(i, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	w.Write(out)
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
			slurp := strings.Replace(string(slurpBytes), secringPlaceholder, secRing, 1)
			return namedReadSeeker{path, strings.NewReader(slurp)}, nil
		},
	}
}

func testConfig(name string, t *testing.T) {
	obj, err := configParser().ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	wantedError := func() error {
		slurp, err := ioutil.ReadFile(strings.Replace(name, ".json", ".err", 1))
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			t.Fatalf("Error reading .err file: %v", err)
		}
		return errors.New(string(slurp))
	}
	lowLevelConf, err := serverconfig.GenLowLevelConfig(&serverconfig.Config{Obj: obj})
	if g, w := strings.TrimSpace(fmt.Sprint(err)), strings.TrimSpace(fmt.Sprint(wantedError())); g != w {
		t.Fatalf("test %s: got GenLowLevelConfig error %q; want %q", name, g, w)
	}
	if err != nil {
		return
	}

	wantFile := strings.Replace(name, ".json", "-want.json", 1)
	wantConf, err := configParser().ReadFile(wantFile)
	if err != nil {
		t.Fatalf("test %s: ReadFile: %v", name, err)
	}
	var got, want bytes.Buffer
	prettyPrint(t, &got, lowLevelConf.Obj, 0)
	prettyPrint(t, &want, wantConf, 0)
	if got.String() != want.String() {
		if *updateGolden {
			contents, err := json.MarshalIndent(lowLevelConf.Obj, "", "\t")
			if err != nil {
				t.Fatal(err)
			}
			secRing, err := filepath.Abs("../jsonsign/testdata/test-secring.gpg")
			contents = bytes.Replace(contents, []byte(secRing),
				[]byte(secringPlaceholder), 1)
			if err := ioutil.WriteFile(wantFile, contents, 0644); err != nil {
				t.Fatal(err)
			}
		}
		t.Errorf("test %s configurations differ.\nGot:\n%s\nWant:\n%s\nDiff (got -> want), %s:\n%s",
			name, &got, &want, name, test.Diff(got.Bytes(), want.Bytes()))
	}
}
