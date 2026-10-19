/*
Copyright 2026 The Perkeep Authors.

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

package quicktime

import (
	"strconv"
	"strings"
)

// ParseISO6709 parses the latitude and longitude, in decimal degrees,
// of an ISO 6709 point such as "+47.6305-122.3478+084.505/", the format
// of KeyLocationISO6709. Any altitude or coordinate reference system
// following the longitude is ignored.
func ParseISO6709(s string) (lat, long float64, ok bool) {
	s = strings.TrimSuffix(s, "/")
	lat, rest, ok := parseISO6709Coord(s, 2)
	if !ok || lat < -90 || lat > 90 {
		return 0, 0, false
	}
	long, rest, ok = parseISO6709Coord(rest, 3)
	if !ok || long < -180 || long > 180 {
		return 0, 0, false
	}
	if rest != "" && rest[0] != '+' && rest[0] != '-' && !strings.HasPrefix(rest, "CRS") {
		return 0, 0, false
	}
	return lat, long, true
}

// parseISO6709Coord parses a signed coordinate at the start of s, whose
// integer degrees have degDigits digits, optionally followed by
// two-digit minutes and seconds, and then an optional decimal fraction
// of the last component.
func parseISO6709Coord(s string, degDigits int) (v float64, rest string, ok bool) {
	if s == "" || s[0] != '+' && s[0] != '-' {
		return 0, "", false
	}
	neg := s[0] == '-'
	i := 1
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	intPart := s[1:i]
	frac := ""
	if i < len(s) && s[i] == '.' {
		j := i + 1
		for j < len(s) && isDigit(s[j]) {
			j++
		}
		if j == i+1 {
			return 0, "", false
		}
		frac = s[i:j]
		i = j
	}
	if len(intPart) < degDigits || len(intPart) > degDigits+4 || (len(intPart)-degDigits)%2 != 0 {
		return 0, "", false
	}
	// The integer digits split into degrees, then minutes, then seconds.
	// Only the last component has a fraction.
	parts := []string{intPart[:degDigits]}
	for p := intPart[degDigits:]; p != ""; p = p[2:] {
		parts = append(parts, p[:2])
	}
	parts[len(parts)-1] += frac
	scale := 1.0
	for n, p := range parts {
		f, err := strconv.ParseFloat(p, 64)
		if err != nil || n > 0 && f >= 60 {
			return 0, "", false
		}
		v += f / scale
		scale *= 60
	}
	if neg {
		v = -v
	}
	return v, s[i:], true
}

func isDigit(c byte) bool { return '0' <= c && c <= '9' }
