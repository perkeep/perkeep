/*
Copyright 2025 The Perkeep Authors

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

package local

import "testing"

func TestGenerationer(t *testing.T) {
	testDir := t.TempDir()
	gen := NewGenerationer(testDir)

	initTime1, random1, err := gen.StorageGeneration()
	if err != nil {
		t.Fatalf("StorageGeneration error: %v", err)
	}

	gen2 := NewGenerationer(testDir)
	initTime2, random2, err := gen2.StorageGeneration()
	if err != nil {
		t.Fatalf("StorageGeneration error: %v", err)
	}

	if !initTime1.Equal(initTime2) {
		t.Fatalf("init times differ: %v vs %v", initTime1, initTime2)
	}
	if random1 != random2 {
		t.Fatalf("random strings differ: %q vs %q", random1, random2)
	}

	if err := gen2.ResetStorageGeneration(); err != nil {
		t.Fatalf("ResetStorageGeneration error: %v", err)
	}

	_, random3, err := gen2.StorageGeneration()
	if err != nil {
		t.Fatalf("StorageGeneration error: %v", err)
	}
	if random3 == random1 {
		t.Fatalf("after reset, random strings equal: %q vs %q", random3, random1)
	}
}
