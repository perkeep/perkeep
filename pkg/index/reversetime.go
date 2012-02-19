/*
Copyright 2011 Google Inc.

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

package index

import (
	"fmt"
	"strings"
)

func unreverseTimeString(s string) string {
	if !strings.HasPrefix(s, "rt") {
		panic(fmt.Sprintf("can't unreverse time string: %q", s))
	}
	b := make([]byte, 0, len(s)-2)
	b = appendReverseString(b, s[2:])
	return string(b)
}

func reverseTimeString(s string) string {
	b := make([]byte, 0, len(s)+2)
	b = append(b, 'r')
	b = append(b, 't')
	b = appendReverseString(b, s)
	return string(b)
}

func appendReverseString(b []byte, s string) []byte {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' {
			b = append(b, '0'+('9'-c))
		} else {
			b = append(b, c)
		}
	}
	return b
}
