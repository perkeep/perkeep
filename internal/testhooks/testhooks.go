/*
Copyright 2018 The Perkeep Authors

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

// Package testhooks is a shared package between Perkeep packages and test code,
// to let tests do gross things that we don't want to expose normally.
package testhooks

import (
	"os"
	"strconv"
)

// UseSHA1 controls whether new blobs use SHA-1 by default.
// This was added because we had a massive pile of tests with SHA-1-based golden data
// and rebasing it all to SHA-224 was too painful.
var useSHA1 bool

func init() {
	if ok, _ := strconv.ParseBool(os.Getenv("CAMLI_SHA1_ENABLED")); ok {
		useSHA1 = true
	}
}

func SetUseSHA1(v bool) (restore func()) {
	old := useSHA1
	setUseSHA1(v)
	return func() { setUseSHA1(old) }
}

func setUseSHA1(v bool) {
	useSHA1 = v
	// For child processes:
	if v {
		os.Setenv("PK_TEST_USE_SHA1", "1")
	} else {
		os.Unsetenv("PK_TEST_USE_SHA1")
	}
}

func UseSHA1() bool {
	return useSHA1 || os.Getenv("PK_TEST_USE_SHA1") == "1"
}
