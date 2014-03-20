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

package test

import (
	"os"
	"strconv"
	"testing"
)

// DependencyErrorOrSkip is called when a test's dependency
// isn't found. It either skips the current test (if SKIP_DEP_TESTS is set),
// or calls t.Error with an error.
func DependencyErrorOrSkip(t *testing.T) {
	b, _ := strconv.ParseBool(os.Getenv("SKIP_DEP_TESTS"))
	if b {
		t.Skip("SKIP_DEP_TESTS is set; skipping test.")
	}
	t.Error("External test dependencies not found, and environment SKIP_DEP_TESTS not set.")
}
