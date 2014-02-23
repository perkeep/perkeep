/*
Copyright 2014 The Camlistore Authors

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

package buildinfo

import (
	"go/build"
	"testing"
)

func TestTestingLinked(t *testing.T) {
	if !isGo12() {
		t.Skip("skipping test for Go 1.1")
	}
	if testingLinked == nil {
		t.Fatal("go1.2+ but testingLinked is nil")
	}
	if !testingLinked() {
		t.Error("testingLinked = false; want true")
	}
}

// isGo12 reports whether the Go version is 1.2 or higher
func isGo12() bool {
	for _, v := range build.Default.ReleaseTags {
		if v == "go1.2" {
			return true
		}
	}
	return false
}
