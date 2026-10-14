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

package importer

import (
	"fmt"
	"strings"
	"testing"
)

func TestRegisterRejectsColons(t *testing.T) {
	for i, name := range []string{":", ":leading", "trailing:", "middle:colon", "multiple::colons"} {
		t.Run(name, func(t *testing.T) {
			alias := fmt.Sprintf("colon-test-alias-%d", i)
			var got any
			func() {
				defer func() { got = recover() }()
				Register(name, &todoImp{Props: Properties{PermanodeImporterType: alias}})
			}()
			if msg, ok := got.(string); !ok || !strings.Contains(msg, "must not contain ':'") {
				t.Fatalf("Register(%q) panic = %v; want colon rejection", name, got)
			}
			if _, ok := importers[name]; ok {
				t.Errorf("Register(%q) added an invalid name", name)
			}
			if reservedImporterKey[alias] {
				t.Errorf("Register(%q) reserved an alias before rejecting the name", name)
			}
		})
	}
}

func TestRegisterAllowsColonFreeNames(t *testing.T) {
	for _, name := range []string{"register-test-dash", "register_test_underscore", "register.test.dot"} {
		t.Run(name, func(t *testing.T) {
			if _, exists := importers[name]; exists {
				t.Fatalf("test name %q already registered", name)
			}
			t.Cleanup(func() { delete(importers, name) })
			Register(name, TODOImporter)
			if got := importers[name]; got != TODOImporter {
				t.Errorf("Register(%q) did not register the importer", name)
			}
		})
	}
}
