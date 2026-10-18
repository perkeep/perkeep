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

package fs

import (
	"strings"
	"testing"

	"tailscale.com/tstest/deptest"
	"tailscale.com/util/set"
)

func TestDeps(t *testing.T) {
	// Like the production package and TestMain, this test is Linux-only.
	// Check the production dependency closure, not the FUSE integration tests.
	// SQLite and localdisk remain allowed; do not ban cloud SDKs or their
	// shared gRPC/protobuf dependencies wholesale.
	badDeps := map[string]string{
		"perkeep.org/pkg/server":        "clients must not depend on server handlers",
		"perkeep.org/pkg/serverinit":    "clients must not assemble a server",
		"perkeep.org/pkg/search":        "clients must use internal/searchtypes instead of the search implementation",
		"perkeep.org/pkg/index":         "clients must not depend on the index",
		"perkeep.org/pkg/webserver":     "client authentication must not depend on the HTTP server implementation",
		"perkeep.org/internal/images":   "clients must not decode or transform images",
		"tailscale.com/tsnet":           "clients use HTTP and do not run a tailnet node",
		"github.com/tetratelabs/wazero": "clients must not embed a WebAssembly runtime",
		"github.com/gen2brain/avif":     "clients must not embed an AVIF decoder",
		"github.com/gen2brain/heic":     "clients must not embed a HEIC decoder",
	}
	for _, target := range []struct{ name, goos, goarch, cgo string }{
		{"native-linux-cgo1", "", "", "1"},
		{"native-linux-cgo0", "", "", "0"},
		{"linux-amd64-cgo0", "linux", "amd64", "0"},
		{"linux-arm64-cgo0", "linux", "arm64", "0"},
	} {
		t.Run(target.name, func(t *testing.T) {
			deptest.DepChecker{
				GOOS:     target.goos,
				GOARCH:   target.goarch,
				ExtraEnv: []string{"CGO_ENABLED=" + target.cgo},
				BadDeps:  badDeps,
				// Ensure these checks inspect the real FUSE implementation.
				WantDeps: set.Of("bazil.org/fuse"),
				OnDep: func(dep string) {
					// BadDeps checks exact packages; also reject their subpackages.
					for root, why := range badDeps {
						if strings.HasPrefix(dep, root+"/") {
							t.Errorf("package %q is not allowed as a dependency: %s", dep, why)
						}
					}
					if strings.HasPrefix(dep, "gvisor.dev/") {
						t.Errorf("package %q is not allowed as a dependency: clients must not embed a userspace network stack", dep)
					}
				},
			}.Check(t)
		})
	}
}
