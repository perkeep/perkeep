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

package types

import (
	"encoding/json"
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"time"
)

var (
	goVersion  = runtime.Version()
	isGo10     = goVersion == "go1" || strings.HasPrefix(runtime.Version(), "go1.0")
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

func parseForGo10(s string) (time.Time, error) {
	var numbers string
	noNanos := dotNumbers.ReplaceAllStringFunc(s, func(m string) string {
		numbers = m
		return ""
	})
	t, err := time.Parse(time.RFC3339, noNanos)
	if err != nil {
		return t, fmt.Errorf("Failed to parse %q as an RFC 3339 time: %v", noNanos, err)
	}
	if numbers != "" {
		nanos, err := time.ParseDuration(numbers + "s")
		if err != nil {
			return t, fmt.Errorf("Failed to parse %q as a duration: %v", numbers+"s", err)
		}
		t = t.Add(nanos)
	}
	return t, nil
}

func (t *Time3339) UnmarshalJSON(b []byte) error {
	if len(b) < 2 || b[0] != '"' || b[len(b)-1] != '"' {
		return fmt.Errorf("types: failed to unmarshal non-string value %q as an RFC 3339 time")
	}
	tm, err := ParseTime3339(string(b[1 : len(b)-1]))
	if err != nil {
		return err
	}
	*t = tm
	return nil
}

// ParseTime3339 parses a string in RFC3339 format.
func ParseTime3339(v string) (t Time3339, err error) {
	var tm time.Time
	if isGo10 {
		tm, err = parseForGo10(v)
	} else {
		tm, err = time.Parse(time.RFC3339Nano, v)
	}
	return Time3339(tm), err
}

// ParseTime3339 parses a string in RFC3339 format. If it's invalid,
// the zero time value is returned instead.
func ParseTime3339OrZero(v string) Time3339 {
	t, err := ParseTime3339(v)
	if err != nil {
		return Time3339{}
	}
	return t
}

func ParseTime3339OrZil(v string) *Time3339 {
	t, err := ParseTime3339(v)
	if err != nil {
		return nil
	}
	return &t
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
