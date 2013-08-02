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

package integration

import (
	"testing"

	"camlistore.org/pkg/test"
)

// Make sure that the camlistored process started
// by the World gets terminated when all the tests
// are done.
// This works only as long as TestZLastTest is the
// last test to run in the package.
func TestZLastTest(t *testing.T) {
	test.GetWorldMaybe(t).Stop()
}
