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

package searchtypes

import (
	"strings"
	"testing"

	"tailscale.com/tstest/deptest"
)

func TestDeps(t *testing.T) {
	// Protocol types and helpers must not pull in server implementations,
	// network stacks, or image decoders. Schema helpers and their blobserver
	// interfaces (via filewriter) remain allowed.
	badDeps := map[string]string{
		"perkeep.org/pkg/server":        "search protocol helpers must not depend on server handlers",
		"perkeep.org/pkg/serverinit":    "search protocol helpers must not assemble a server",
		"perkeep.org/pkg/search":        "search protocol helpers must not depend on the search implementation",
		"perkeep.org/pkg/index":         "search protocol helpers must not depend on the index",
		"perkeep.org/pkg/webserver":     "search protocol helpers must not depend on the HTTP server implementation",
		"perkeep.org/internal/images":   "search protocol helpers must not decode or transform images",
		"tailscale.com/tsnet":           "search protocol helpers must not run a tailnet node",
		"github.com/tetratelabs/wazero": "search protocol helpers must not embed a WebAssembly runtime",
		"github.com/gen2brain/avif":     "search protocol helpers must not embed an AVIF decoder",
		"github.com/gen2brain/heic":     "search protocol helpers must not embed a HEIC decoder",
	}
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
				BadDeps:  badDeps,
				OnDep: func(dep string) {
					// BadDeps checks exact packages; also reject their subpackages.
					for root, why := range badDeps {
						if strings.HasPrefix(dep, root+"/") {
							t.Errorf("package %q is not allowed as a dependency: %s", dep, why)
						}
					}
					if strings.HasPrefix(dep, "gvisor.dev/") {
						t.Errorf("package %q is not allowed as a dependency: search protocol helpers must not embed a userspace network stack", dep)
					}
				},
			}.Check(t)
		})
	}
}
