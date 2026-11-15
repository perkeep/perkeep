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
	"time"
)

// GitInfo is either the empty string (the default)
// or is set to the git hash of the most recent commit
// using the -X linker flag. For example, it's set like:
// $ go install --ldflags="-X perkeep.org/pkg/buildinfo.GitInfo "`./misc/gitversion` perkeep.org/server/perkeepd
var GitInfo string

// Version is a string like "0.10" or "1.0", if applicable.
var Version string

// Summary returns the version and/or git version of this binary. The git
// version is GitInfo, if the linker flags set it, as make.go does, or else
// the commit that the go command recorded building the binary in a git
// checkout, in the same form. Without either, it's "unknown".
func Summary() string {
	gitInfo := GitInfo
	if gitInfo == "" {
		if bi, ok := debug.ReadBuildInfo(); ok {
			gitInfo = vcsGitInfo(bi.Settings)
		}
	}
	if Version != "" && gitInfo != "" {
		return Version + ", " + gitInfo
	}
	if gitInfo != "" {
		return gitInfo
	}
	if Version != "" {
		return Version
	}
	return "unknown"
}

// vcsGitInfo returns the git version that the go command's VCS build
// settings give, in make.go's form for GitInfo, "yyyy-mm-dd-" and the
// first 10 hex digits of the commit, and "+" if the tree had uncommitted
// changes, or the empty string if they don't give a git commit.
func vcsGitInfo(settings []debug.BuildSetting) string {
	var vcs, rev, commitTime string
	var modified bool
	for _, s := range settings {
		switch s.Key {
		case "vcs":
			vcs = s.Value
		case "vcs.revision":
			rev = s.Value
		case "vcs.time":
			commitTime = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if vcs != "git" || len(rev) < 10 {
		return ""
	}
	v := rev[:10]
	if t, err := time.Parse(time.RFC3339, commitTime); err == nil {
		v = t.Format("2006-01-02") + "-" + v
	}
	if modified {
		v += "+"
	}
	return v
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

	// HEIC is how HEIC images are decoded: "h265" for the pure Go
	// decoder of github.com/gen2brain/h265. It's empty if HEIC support
	// isn't linked in.
	HEIC string `json:"heic"`
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

// HEICStatus returns a plaintext (non-HTML) string describing the HEIC
// image decoder.
func HEICStatus() string {
	f := CurrentFeatures()
	switch f.HEIC {
	case "":
		return "none"
	case "h265":
		return "github.com/gen2brain/h265 (pure Go)"
	}
	return f.HEIC
}
