/*
Copyright 2013 The Camlistore Authors

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

// Package android contains code specific to running the Camlistore client
// code as a child process on Android. This removes ugly API from the
// client package itself.
package android

import (
	"fmt"
	"strconv"
	"os"
	"sync"
)

// TODO(mpl): distinguish CAMPUT, CAMGET, etc
var androidOutput, _ = strconv.ParseBool(os.Getenv("CAMPUT_ANDROID_OUTPUT"))

// IsChild reports whether this process is running as an Android
// child process and should report its output in the form that the
// Android uploader app expects.
func IsChild() bool {
	return androidOutput
}

var androidOutMu sync.Mutex

func Printf(format string, args ...interface{}) {
	androidOutMu.Lock()
	defer androidOutMu.Unlock()
	fmt.Printf(format, args...)
}
