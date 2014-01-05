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

// Package strutil contains string and byte processing functions.
package strutil

import "strings"

// Fork of Go's implementation in pkg/strings/strings.go:
// Generic split: splits after each instance of sep,
// including sepSave bytes of sep in the subarrays.
func genSplit(dst []string, s, sep string, sepSave, n int) []string {
	if n == 0 {
		return nil
	}
	if sep == "" {
		panic("sep is empty")
	}
	if n < 0 {
		n = strings.Count(s, sep) + 1
	}
	c := sep[0]
	start := 0
	na := 0
	for i := 0; i+len(sep) <= len(s) && na+1 < n; i++ {
		if s[i] == c && (len(sep) == 1 || s[i:i+len(sep)] == sep) {
			dst = append(dst, s[start:i+sepSave])
			na++
			start = i + len(sep)
			i += len(sep) - 1
		}
	}
	dst = append(dst, s[start:])
	return dst
}

// AppendSplitN is like strings.SplitN but appends to and returns dst.
// Unlike strings.SplitN, an empty separator is not supported.
// The count n determines the number of substrings to return:
//   n > 0: at most n substrings; the last substring will be the unsplit remainder.
//   n == 0: the result is nil (zero substrings)
//   n < 0: all substrings
func AppendSplitN(dst []string, s, sep string, n int) []string {
	return genSplit(dst, s, sep, 0, n)
}

// HasPrefixFold is like strings.HasPrefix but uses Unicode case-folding.
func HasPrefixFold(s, prefix string) bool {
	// TODO: Remove assumption that both strings have the same byte length.
	if len(s) < len(prefix) {
		return false
	}
	return strings.EqualFold(s[:len(prefix)], prefix)
}

// HasSuffixFold is like strings.HasPrefix but uses Unicode case-folding.
func HasSuffixFold(s, suffix string) bool {
	// TODO: Remove assumption that both strings have the same byte length.
	if len(s) < len(suffix) {
		return false
	}
	return strings.EqualFold(s[len(s)-len(suffix):], suffix)
}

// ContainsFold is like strings.Contains but (ought to) use Unicode case-folding.
func ContainsFold(s, substr string) bool {
	// TODO: Make this not do allocations.
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
