/*
Copyright 2017 The Camlistore Authors.

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

package main

import (
	"sort"
	"strings"
)

type separatedString []string

// newSeparatedString returns a newly created SeparatedString value from the string given.
// There is no error if the string does not include a comma.
func newSeparatedString(commaSeparated string) separatedString {
	if strings.TrimSpace(commaSeparated) == "" {
		return nil
	}
	split := strings.Split(commaSeparated, ",")
	var ss []string
	for i, tag := range split {
		split[i] = strings.TrimSpace(tag)
		if split[i] != "" {
			ss = append(ss, split[i])
		}
	}
	if !sort.StringsAreSorted(ss) {
		sort.Strings(ss)
	}
	return ss
}

// String formats the string in the canonical comma-separated manner
func (css separatedString) String() string {
	return strings.Join(css, ", ")
}

func (css separatedString) isEmpty() bool {
	if len(css) == 0 {
		return true
	}
	if len(css) == 1 && css[0] == "" {
		return true
	}
	return false
}

// Minus returns a newly created SeparatedString containing all the elements from css that did
// not appear in css2
func (css separatedString) Minus(css2 separatedString) separatedString {
	if !sort.StringsAreSorted(css) {
		sort.Strings(css)
	}
	if !sort.StringsAreSorted(css2) {
		sort.Strings(css2)
	}
	k := 0
	kMax := len(css2)
	keep := css[:0]
	for _, s := range css {
		for k < kMax && css2[k] < s {
			k++
		}
		if k == kMax || css2[k] != s {
			keep = append(keep, s)
		}
	}
	return keep
}

// equal compares css with css2. Both css and css2 are sorted in place before being compared.
func (css separatedString) equal(css2 separatedString) bool {
	if len(css) != len(css2) {
		return false
	}
	if !sort.StringsAreSorted(css) {
		sort.Strings(css)
	}
	if !sort.StringsAreSorted(css2) {
		sort.Strings(css2)
	}
	for k, s := range css {
		if s != css2[k] {
			return false
		}
	}
	return true
}
