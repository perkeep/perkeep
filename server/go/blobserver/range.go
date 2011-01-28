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

package main

import (
	"http"
	"regexp"
	"strconv"
)

// Default is {0, -1} to read all of a file.
type requestedRange struct {
	SkipBytes int64
	LimitBytes int64  // or -1 to read all
}

func (rr *requestedRange) IsWholeFile() bool {
	return rr.SkipBytes == 0 && rr.LimitBytes == -1;
}

var wholeRange = &requestedRange{0, -1}

var rangePattern = regexp.MustCompile(`bytes=([0-9]+)-([0-9]*)`)

func getRequestedRange(req *http.Request) *requestedRange {
	rrange, ok := req.Header["Range"]
	if !ok {
		return wholeRange
	}
	return getRequestedRangeFromString(rrange)
}

func getRequestedRangeFromString(rrange string) *requestedRange {
	matches := rangePattern.FindStringSubmatch(rrange)
	if len(matches) == 0 {
		return wholeRange;
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
	return &requestedRange{skipBytes, limitBytes}
}
