/*
Copyright 2013 Google Inc.

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

// Hacks for running camput as a child process on Android.

package main

import (
	"camlistore.org/pkg/client/android"
)

type allStats struct {
	total, skipped, uploaded stats
}

var lastStatBroadcast allStats

func printAndroidCamputStatus(t *TreeUpload) {
	bcast := allStats{t.total, t.skipped, t.uploaded}
	if bcast == lastStatBroadcast {
		return
	}
	lastStatBroadcast = bcast

	android.Printf("STATS nfile=%d nbyte=%d skfile=%d skbyte=%d upfile=%d upbyte=%d\n",
		t.total.files, t.total.bytes,
		t.skipped.files, t.skipped.bytes,
		t.uploaded.files, t.uploaded.bytes)
}
