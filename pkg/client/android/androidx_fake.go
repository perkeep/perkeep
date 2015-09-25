// +build fake_android

/*
Copyright 2015 The Camlistore Authors.

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

package android

// If non-nil, user-provided hook used by OnAndroid.
var OnAndroidHook func() bool

// IsChild reports whether this process is running as an Android
// child process and should report its output in the form that the
// Android uploader app expects.
func IsChild() bool {
	if OnAndroidHook != nil {
		return OnAndroidHook()
	}
	return false
}

func OnAndroid() bool {
	if OnAndroidHook != nil {
		return OnAndroidHook()
	}
	return false
}
