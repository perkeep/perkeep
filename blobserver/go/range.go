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
	matches := rangePattern.MatchStrings(rrange)
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
