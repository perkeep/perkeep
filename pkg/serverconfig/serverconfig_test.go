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
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/serverconfig"
)

func prettyPrint(i interface{}, indent int) {
	switch ei := i.(type) {
	case jsonconfig.Obj:
		for k, v := range ei {
			fmt.Printf("\n")
			fmt.Printf("%s: ", k)
			prettyPrint(v, indent)
		}
		fmt.Printf("\n")
	case map[string]interface{}:
		indent++
		for k, v := range ei {
			fmt.Printf("\n")
			for i := 0; i < indent; i++ {
				fmt.Printf("	")
			}
			fmt.Printf("%s: ", k)
			prettyPrint(v, indent)
		}
	case []interface{}:
		fmt.Printf("	")
		for _, v := range ei {
			prettyPrint(v, indent)
		}
	default:
		fmt.Printf("%v, ", i)
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

func testConfig(name string, t *testing.T) {
	obj, err := jsonconfig.ReadFile("testdata/default.json")
	if err != nil {
		t.Fatal(err)
	}
	lowLevelConf, err := serverconfig.GenLowLevelConfig(&serverconfig.Config{jsonconfig.Obj: obj})
	if err != nil {
		t.Fatal(err)
	}
	wantConf, err := jsonconfig.ReadFile("testdata/default-want.json")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(lowLevelConf.Obj, wantConf) {
		fmt.Printf("Configurations differ:\n")
		fmt.Printf("Generated:")
		prettyPrint(lowLevelConf.Obj, 0)
		fmt.Printf("\nWant:")
		prettyPrint(wantConf, 0)
		t.Fail()
	}
}
