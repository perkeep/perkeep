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

package sqlite

import (
	"strings"
	"testing"
)

func TestInitDBRequiresWAL(t *testing.T) {
	// An in-memory database cannot use WAL. InitDB must return its error to
	// the caller instead of terminating the process with log.Fatal.
	err := InitDB(":memory:")
	if err == nil || !strings.Contains(err.Error(), "SQLite Write Ahead Logging") {
		t.Fatalf("InitDB(:memory:) = %v; want WAL requirement error", err)
	}
}
