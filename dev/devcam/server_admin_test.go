/*
Copyright 2026 The Perkeep Authors.

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

package main

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestIndexerSetupCommand(t *testing.T) {
	for _, backend := range []string{"mysql", "postgres", "sqlite", "none"} {
		for _, wipe := range []bool{false, true} {
			t.Run(backend+"/"+map[bool]string{false: "keep", true: "wipe"}[wipe], func(t *testing.T) {
				c := &serverCmd{
					mysql: backend == "mysql", postgres: backend == "postgres",
					sqlite: backend == "sqlite", wipe: wipe,
					env: &Env{m: map[string]string{"CAMLI_DBNAME": "test-index"}},
				}
				cmd := c.indexerSetupCommand()
				if backend == "none" {
					if cmd != nil {
						t.Fatalf("non-SQL index command = %v; want nil", cmd)
					}
					return
				}
				want := []string{filepath.Join("bin", "perkeepd"), "admin", "dbinit"}
				switch backend {
				case "mysql":
					want = append(want, "-user=root", "-password=root", "-host=localhost", "-dbname=test-index")
				case "postgres":
					want = append(want, "-dbtype=postgres", "-user=postgres", "-password=postgres", "-host=localhost", "-dbname=test-index")
				case "sqlite":
					want = append(want, "-dbtype=sqlite", "-dbname=test-index")
				}
				if wipe {
					want = append(want, "-wipe")
				} else {
					want = append(want, "-ignoreexists")
				}
				if !slices.Equal(cmd.Args, want) {
					t.Errorf("command args = %q; want %q", cmd.Args, want)
				}
			})
		}
	}
}
