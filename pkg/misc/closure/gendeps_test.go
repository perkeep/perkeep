/*
Copyright 2013 The Camlistore Authors.

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

package closure

import (
	"reflect"
	"strings"
	"testing"
)

var testdata = `
goog.addDependency('asserts/asserts.js', ['goog.asserts', 'goog.asserts.AssertionError'], ['goog.debug.Error', 'goog.string']);
goog.addDependency('debug/error.js', ['goog.debug.Error'], []);
goog.addDependency('string/string.js', ['goog.string', 'goog.string.Unicode'], []);
`

type parsedDeps struct {
	providedBy map[string]string
	requires   map[string][]string
}

var parsedWant = parsedDeps{
	providedBy: map[string]string{
		"goog.asserts":                "asserts/asserts.js",
		"goog.asserts.AssertionError": "asserts/asserts.js",
		"goog.debug.Error":            "debug/error.js",
		"goog.string":                 "string/string.js",
		"goog.string.Unicode":         "string/string.js",
	},
	requires: map[string][]string{
		"goog.asserts":                []string{"goog.debug.Error", "goog.string"},
		"goog.asserts.AssertionError": []string{"goog.debug.Error", "goog.string"},
	},
}

var deepParsedWant = map[string][]string{
	"goog.asserts":                []string{"asserts/asserts.js", "debug/error.js", "string/string.js"},
	"goog.asserts.AssertionError": []string{"asserts/asserts.js", "debug/error.js", "string/string.js"},
	"goog.debug.Error":            []string{"debug/error.js"},
	"goog.string":                 []string{"string/string.js"},
	"goog.string.Unicode":         []string{"string/string.js"},
}

func TestParseDeps(t *testing.T) {
	providedBy, requires, err := ParseDeps(strings.NewReader(testdata))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(parsedWant.providedBy, providedBy) {
		t.Fatalf("Failed to parse closure deps: wanted %v, got %v", parsedWant.providedBy, providedBy)
	}
	if !reflect.DeepEqual(parsedWant.requires, requires) {
		t.Fatalf("Failed to parse closure deps: wanted %v, got %v", parsedWant.requires, requires)
	}
}

func TestDeepParseDeps(t *testing.T) {
	deps, err := DeepParseDeps(strings.NewReader(testdata))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(deepParsedWant, deps) {
		t.Fatalf("Failed to parse closure deps: wanted %v, got %v", deepParsedWant, deps)
	}
}
