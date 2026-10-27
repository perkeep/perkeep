/*
Copyright 2014 The Perkeep Authors

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

// Package buildinfo provides information about the current build.
package buildinfo // import "perkeep.org/pkg/buildinfo"

import (
	"flag"
	"runtime"
	"runtime/debug"
)

// GitInfo is either the empty string (the default)
// or is set to the git hash of the most recent commit
// using the -X linker flag. For example, it's set like:
// $ go install --ldflags="-X perkeep.org/pkg/buildinfo.GitInfo "`./misc/gitversion` perkeep.org/server/perkeepd
var GitInfo string

// Version is a string like "0.10" or "1.0", if applicable.
var Version string

// Summary returns the version and/or git version of this binary.
// If the linker flags were not provided, the return value is "unknown".
func Summary() string {
	if Version != "" && GitInfo != "" {
		return Version + ", " + GitInfo
	}
	if GitInfo != "" {
		return GitInfo
	}
	if Version != "" {
		return Version
	}
	return "unknown"
}

// TestingLinked reports whether the "testing" package is linked into the binary.
func TestingLinked() bool {
	return flag.CommandLine.Lookup("test.v") != nil
}

// Features describes the optional and environment-dependent features of
// the running binary. It's what perkeepd -build-config prints, as JSON.
type Features struct {
	Version   string `json:"version"`   // as returned by Summary
	GoVersion string `json:"goVersion"` // as returned by runtime.Version
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	CGO       bool   `json:"cgo"` // whether built with CGO_ENABLED=1

	// HEIC is how HEIC images are decoded: "libheif" for the system's
	// libheif, loaded at runtime, or "wasm" for the slower embedded
	// WebAssembly build of it. It's empty if HEIC support isn't linked in.
	HEIC string `json:"heic"`
	// LibheifError, if HEIC is "wasm", is why libheif wasn't used.
	LibheifError string `json:"libheifError,omitempty"`
}

var featureFuncs []func(*Features)

// RegisterFeatureFunc registers fn to fill in the Features fields for a
// package's optional feature, when that package is linked in.
// It must be called from an init function.
func RegisterFeatureFunc(fn func(*Features)) {
	featureFuncs = append(featureFuncs, fn)
}

// CurrentFeatures returns the features of the running binary.
func CurrentFeatures() Features {
	f := Features{
		Version:   Summary(),
		GoVersion: runtime.Version(),
		GOOS:      runtime.GOOS,
		GOARCH:    runtime.GOARCH,
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "CGO_ENABLED" {
				f.CGO = s.Value == "1"
			}
		}
	}
	for _, fn := range featureFuncs {
		fn(&f)
	}
	return f
}

// HEICStatus returns a plaintext (non-HTML) string describing the
// state of HEIC image decoder variant.
func HEICStatus() string {
	f := CurrentFeatures()
	switch f.HEIC {
	case "":
		return "unknown"
	case "libheif":
		return "libheif available (optimized)"
	}
	return "libheif not available (using WASM fallback): " + f.LibheifError
}
