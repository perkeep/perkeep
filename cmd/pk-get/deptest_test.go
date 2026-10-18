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

func TestDeps(t *testing.T) {
	for _, target := range []struct{ name, goos, goarch string }{
		{"native", "", ""},
		{"linux/amd64", "linux", "amd64"},
		{"darwin/arm64", "darwin", "arm64"},
		{"windows/amd64", "windows", "amd64"},
	} {
		t.Run(target.name, func(t *testing.T) {
			// Preserve the ordinary host build's cgo setting.
			var extraEnv []string
			if target.goos != "" {
				extraEnv = []string{"CGO_ENABLED=0"}
			}
			deptest.DepChecker{
				GOOS:     target.goos,
				GOARCH:   target.goarch,
				ExtraEnv: extraEnv,
				BadDeps: map[string]string{
					"tailscale.com/tsnet": "clients use HTTP and do not run a tailnet node",
				},
				OnImport: func(imp string) {
					// Guard pk-get's own sniffing dependency, not the
					// client -> search -> index path left for a later change.
					if imp == "perkeep.org/pkg/index" {
						t.Errorf("package %q is not allowed as a direct import: use internal/blobsniffer for blob sniffing", imp)
					}
				},
				OnDep: func(dep string) {
					if strings.HasPrefix(dep, "tailscale.com/tsnet/") || strings.HasPrefix(dep, "gvisor.dev/") {
						t.Errorf("package %q is not allowed as a dependency: clients must not embed a tailnet node or userspace network stack", dep)
					}
				},
			}.Check(t)
		})
	}
}
