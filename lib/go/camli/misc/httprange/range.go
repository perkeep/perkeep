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

package httprange

import (
	"http"
	"regexp"
	"strconv"
)

// Default is {0, -1} to read all of a file.
type Range struct {
	skipBytes  int64
	limitBytes int64 // or -1 to read all
}

func (rr *Range) SkipBytes() int64 {
	return rr.skipBytes
}

// LimitBytes reads the max number of bytes to read, or -1 for no limit.
func (rr *Range) LimitBytes() int64 {
	return rr.limitBytes
}

func (rr *Range) IsWholeFile() bool {
	return rr.skipBytes == 0 && rr.limitBytes == -1
}

var WholeRange = &Range{0, -1}

var rangePattern = regexp.MustCompile(`bytes=([0-9]+)-([0-9]*)`)

func FromRequest(req *http.Request) *Range {
	rrange := req.Header.Get("Range")
	if rrange == "" {
		return WholeRange
	}
	return FromString(rrange)
}

func FromString(rrange string) *Range {
	matches := rangePattern.FindStringSubmatch(rrange)
	if len(matches) == 0 {
		return WholeRange
	}
	skipBytes, _ := strconv.Atoi64(matches[1])
	lastByteInclusive := int64(-1)
	if len(matches[2]) > 0 {
		lastByteInclusive, _ = strconv.Atoi64(matches[2])
	}
	limitBytes := int64(-1)
	if lastByteInclusive != -1 {
		limitBytes = lastByteInclusive - skipBytes + 1
		if limitBytes < 0 {
			limitBytes = 0
		}
	}
	return &Range{skipBytes, limitBytes}
}
