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

import "flag"

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

// djpegFunc implements DjpegStatus. nil means the images/fastjpeg package
// isn't linked in.
var djpegFunc func() string

func RegisterDjpegStatusFunc(fn func() string) {
	djpegFunc = fn
}

// DjpegStatus returns a plaintext (non-HTML) string describing the
// state of djpeg on the system.
func DjpegStatus() string {
	if djpegFunc != nil {
		return djpegFunc()
	}
	return "unknown"
}
