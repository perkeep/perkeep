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

package serverinit_test

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
	"camlistore.org/pkg/serverinit"
	"camlistore.org/pkg/test"
	"camlistore.org/pkg/types/serverconfig"
)

var (
	updateGolden = flag.Bool("update_golden", false, "Update golden *.want files")
	flagOnly     = flag.String("only", "", "If non-empty, substring of foo.json input file to match.")
)

const (
	// relativeRing points to a real secret ring, but serverinit
	// rewrites it to be an absolute path.  We then canonicalize
	// it to secringPlaceholder in the golden files.
	relativeRing       = "../jsonsign/testdata/test-secring.gpg"
	secringPlaceholder = "/path/to/secring"
)

func init() {
	// Avoid Linux vs. OS X differences in tests.
	serverinit.SetTempDirFunc(func() string { return "/tmp" })
	serverinit.SetNoMkdir(true)
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
		if *flagOnly != "" && !strings.Contains(name, *flagOnly) {
			continue
		}
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

// configParser returns a custom jsonconfig ConfigParser whose reader rewrites "/path/to/secring" to the absolute path of the jsonconfig test-secring.gpg file.
func configParser() *jsonconfig.ConfigParser {
	return &jsonconfig.ConfigParser{
		Open: func(path string) (jsonconfig.File, error) {
			slurp, err := replaceRingPath(path)
			if err != nil {
				return nil, err
			}
			return namedReadSeeker{path, bytes.NewReader(slurp)}, nil
		},
	}
}

// replaceRingPath returns the contents of the file at path with secringPlaceholder replaced with the absolute path of relativeRing.
func replaceRingPath(path string) ([]byte, error) {
	secRing, err := filepath.Abs(relativeRing)
	if err != nil {
		return nil, fmt.Errorf("Could not get absolute path of %v: %v", relativeRing, err)
	}
	slurpBytes, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return bytes.Replace(slurpBytes, []byte(secringPlaceholder), []byte(secRing), 1), nil
}

func testConfig(name string, t *testing.T) {
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
	b, err := replaceRingPath(name)
	if err != nil {
		t.Fatalf("Could not read %s: %v", name, err)
	}
	var hiLevelConf serverconfig.Config
	if err := json.Unmarshal(b, &hiLevelConf); err != nil {
		t.Fatalf("Could not unmarshal %s into a serverconfig.Config: %v", name, err)
	}

	lowLevelConf, err := serverinit.GenLowLevelConfig(&hiLevelConf)
	if g, w := strings.TrimSpace(fmt.Sprint(err)), strings.TrimSpace(fmt.Sprint(wantedError())); g != w {
		t.Fatalf("test %s: got GenLowLevelConfig error %q; want %q", name, g, w)
	}
	if err != nil {
		return
	}
	if err := (&jsonconfig.ConfigParser{}).CheckTypes(lowLevelConf.Obj); err != nil {
		t.Fatalf("Error while parsing low-level conf generated from %v: %v", name, err)
	}

	wantFile := strings.Replace(name, ".json", "-want.json", 1)
	wantConf, err := configParser().ReadFile(wantFile)
	if err != nil {
		t.Fatalf("test %s: ReadFile: %v", name, err)
	}
	var got, want bytes.Buffer
	prettyPrint(t, &got, lowLevelConf.Obj, 0)
	prettyPrint(t, &want, wantConf, 0)
	if *updateGolden {
		contents, err := json.MarshalIndent(lowLevelConf.Obj, "", "\t")
		if err != nil {
			t.Fatal(err)
		}
		contents = canonicalizeGolden(t, contents)
		if err := ioutil.WriteFile(wantFile, contents, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if got.String() != want.String() {
		t.Errorf("test %s configurations differ.\nGot:\n%s\nWant:\n%s\nDiff (want -> got), %s:\n%s",
			name, &got, &want, name, test.Diff(want.Bytes(), got.Bytes()))
	}
}

func canonicalizeGolden(t *testing.T, v []byte) []byte {
	localPath, err := filepath.Abs(relativeRing)
	if err != nil {
		t.Fatal(err)
	}
	v = bytes.Replace(v, []byte(localPath), []byte(secringPlaceholder), 1)
	if !bytes.HasSuffix(v, []byte("\n")) {
		v = append(v, '\n')
	}
	return v
}
