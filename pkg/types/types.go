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

// Package types provides various common types.
package types

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"runtime"
	"time"
)

var (
	goVersion  = runtime.Version()
	dotNumbers = regexp.MustCompile(`\.\d+`)
)

// Time3339 is a time.Time which encodes to and from JSON
// as an RFC 3339 time in UTC.
type Time3339 time.Time

var (
	_ json.Marshaler   = Time3339{}
	_ json.Unmarshaler = (*Time3339)(nil)
)

func (t Time3339) String() string {
	return time.Time(t).UTC().Format(time.RFC3339Nano)
}

func (t Time3339) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.String())
}

func (t *Time3339) UnmarshalJSON(b []byte) error {
	if len(b) < 2 || b[0] != '"' || b[len(b)-1] != '"' {
		return fmt.Errorf("types: failed to unmarshal non-string value %q as an RFC 3339 time")
	}
	tm, err := time.Parse(time.RFC3339Nano, string(b[1:len(b)-1]))
	if err != nil {
		return err
	}
	*t = Time3339(tm)
	return nil
}

// ParseTime3339OrZero parses a string in RFC3339 format. If it's invalid,
// the zero time value is returned instead.
func ParseTime3339OrZero(v string) Time3339 {
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return Time3339{}
	}
	return Time3339(t)
}

func ParseTime3339OrZil(v string) *Time3339 {
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return nil
	}
	tm := Time3339(t)
	return &tm
}

// Time returns the time as a time.Time with slightly less stutter
// than a manual conversion.
func (t Time3339) Time() time.Time {
	return time.Time(t)
}

// IsZero returns whether the time is Go zero or Unix zero.
func (t *Time3339) IsZero() bool {
	return t == nil || time.Time(*t).IsZero() || time.Time(*t).Unix() == 0
}

// ByTime sorts times.
type ByTime []time.Time

func (s ByTime) Len() int           { return len(s) }
func (s ByTime) Less(i, j int) bool { return s[i].Before(s[j]) }
func (s ByTime) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

// A ReadSeekCloser can Read, Seek, and Close.
type ReadSeekCloser interface {
	io.Reader
	io.Seeker
	io.Closer
}

type ReaderAtCloser interface {
	io.ReaderAt
	io.Closer
}
