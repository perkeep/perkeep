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

package images

import (
	"testing"

	"github.com/gen2brain/heic"
)

// forEachHEICDecoder runs f as a subtest with each of the heic package's
// decoders: the system's libheif, if it can be loaded, and the embedded
// WebAssembly one.
func forEachHEICDecoder(t *testing.T, f func(t *testing.T)) {
	t.Run("libheif", func(t *testing.T) {
		if err := heic.Dynamic(); err != nil {
			t.Skipf("libheif not available: %v", err)
		}
		f(t)
	})
	t.Run("wasm", func(t *testing.T) {
		heic.ForceWasmMode = true
		defer func() { heic.ForceWasmMode = false }()
		f(t)
	})
}
