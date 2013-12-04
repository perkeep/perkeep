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

package strutil

// StringFromBytes returns string(v), minimizing copies for common values of v.
func StringFromBytes(v []byte) string {
	// From net/textproto's reader.go...
	if len(v) == 0 {
		return ""
	}
	lo, hi := 0, len(commonStrings)
	for i, c := range v {
		if lo < hi {
			for lo < hi && (len(commonStrings[lo]) <= i || commonStrings[lo][i] < c) {
				lo++
			}
			for hi > lo && commonStrings[hi-1][i] > c {
				hi--
			}
		} else {
			break
		}
	}
	if lo < hi && len(commonStrings[lo]) == len(v) {
		return commonStrings[lo]
	}
	return string(v)
}

// NOTE: must be sorted
var commonStrings = []string{
	"claim",
	"directory",
	"file",
	"permanode",
	"static-set",
}
