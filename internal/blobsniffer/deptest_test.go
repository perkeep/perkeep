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

package blobsniffer

import (
	"strings"
	"testing"

	"tailscale.com/tstest/deptest"
)

func TestDeps(t *testing.T) {
	// Unlike pk-get's direct-import guard, this checks the sniffer's entire
	// production dependency closure. MIME/header recognition and schema parsing
	// must not pull in servers, network stacks, or image decoder implementations.
	// Lightweight magic, go4.org, and schema/EXIF helpers remain allowed.
	badDeps := map[string]string{
		"perkeep.org/pkg/server":        "blob sniffing must not depend on server handlers",
		"perkeep.org/pkg/serverinit":    "blob sniffing must not assemble a server",
		"perkeep.org/pkg/search":        "blob sniffing must not depend on search",
		"perkeep.org/pkg/index":         "blob sniffing must not depend on the index",
		"perkeep.org/pkg/webserver":     "blob sniffing must not depend on the HTTP server implementation",
		"perkeep.org/internal/images":   "MIME recognition must not decode or transform images",
		"tailscale.com/tsnet":           "blob sniffing must not run a tailnet node",
		"github.com/tetratelabs/wazero": "blob sniffing must not embed a WebAssembly runtime",
		"github.com/gen2brain/avif":     "MIME recognition must not embed an AVIF decoder",
		"github.com/gen2brain/heic":     "MIME recognition must not embed a HEIC decoder",
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
						t.Errorf("package %q is not allowed as a dependency: blob sniffing must not embed a userspace network stack", dep)
					}
				},
			}.Check(t)
		})
	}
}
