/*
Copyright 2013 The Camlistore Authors

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

package sorted_test

import (
	"testing"

	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/sorted/kvtest"
)

func TestMemoryKV(t *testing.T) {
	kv := sorted.NewMemoryKeyValue()
	kvtest.TestSorted(t, kv)
}

// TODO(mpl): move this test into kvtest. But that might require
// kvtest taking a "func () sorted.KeyValue) constructor param,
// so kvtest can create several and close in different ways.
func TestMemoryKV_DoubleClose(t *testing.T) {
	kv := sorted.NewMemoryKeyValue()

	it := kv.Find("", "")
	it.Close()
	it.Close()

	kv.Close()
	kv.Close()
}
