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

package main

import (
	"strings"
	"testing"

	"tailscale.com/tstest/deptest"
)

func TestCLIBackendDeps(t *testing.T) {
	// SQLite remains allowed for client features. Search/index/image
	// implementations still arrive through shared client types; those
	// dependency paths will be addressed separately.
	for _, target := range []struct{ name, goos, goarch string }{
		{"native", "", ""},
		{"linux/amd64", "linux", "amd64"},
		{"darwin/arm64", "darwin", "arm64"},
		{"windows/amd64", "windows", "amd64"},
	} {
		t.Run(target.name, func(t *testing.T) {
			var extraEnv []string
			if target.goos != "" {
				extraEnv = []string{"CGO_ENABLED=0"}
			}
			deptest.DepChecker{
				GOOS:     target.goos,
				GOARCH:   target.goarch,
				ExtraEnv: extraEnv,
				BadDeps: map[string]string{
					"perkeep.org/pkg/blobserver/s3":           "storage migration backends belong in perkeepd admin copy-storage",
					"perkeep.org/pkg/blobserver/google/drive": "storage migration backends belong in perkeepd admin copy-storage",
					"perkeep.org/pkg/blobserver/blobpacked":   "packed storage migration belongs in perkeepd admin copy-storage",
					"perkeep.org/pkg/sorted/mysql":            "database setup belongs in perkeepd admin dbinit",
					"perkeep.org/pkg/sorted/postgres":         "database setup belongs in perkeepd admin dbinit",
					"github.com/go-sql-driver/mysql":          "database setup belongs in perkeepd admin dbinit",
					"github.com/jackc/pgx/v5/stdlib":          "database setup belongs in perkeepd admin dbinit",
					"perkeep.org/pkg/blobserver/diskpacked":   "local storage recovery belongs in perkeepd admin reindex-diskpacked",
					"perkeep.org/pkg/serverinit":              "server configuration belongs in perkeepd admin",
				},
				OnDep: func(dep string) {
					for _, prefix := range []string{
						"github.com/aws/aws-sdk-go-v2",
						"github.com/aws/smithy-go",
						"google.golang.org/api/drive/",
					} {
						if strings.HasPrefix(dep, prefix) {
							t.Errorf("package %q is not allowed as a dependency: backend SDKs belong in perkeepd admin copy-storage", dep)
						}
					}
				},
			}.Check(t)
		})
	}
}
