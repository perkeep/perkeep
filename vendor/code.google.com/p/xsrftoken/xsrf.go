// Copyright 2012 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package xsrftoken provides methods for generating and validating secure XSRF tokens.
package xsrftoken

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The duration that XSRF tokens are valid.
// It is exported so clients may set cookie timeouts that match generated tokens.
const Timeout = 24 * time.Hour

// clean sanitizes a string for inclusion in a token by replacing all ":"s.
func clean(s string) string {
	return strings.Replace(s, ":", "_", -1)
}

// Generate returns a URL-safe secure XSRF token that expires in 24 hours.
//
// key is a secret key for your application.
// userID is a unique identifier for the user.
// actionID is the action the user is taking (e.g. POSTing to a particular path).
func Generate(key, userID, actionID string) string {
	return generateAtTime(key, userID, actionID, time.Now())
}

// generateAtTime is like Generate, but returns a token that expires 24 hours from now.
func generateAtTime(key, userID, actionID string, now time.Time) string {
	h := hmac.New(sha1.New, []byte(key))
	fmt.Fprintf(h, "%s:%s:%d", clean(userID), clean(actionID), now.UnixNano())
	tok := fmt.Sprintf("%s:%d", h.Sum(nil), now.UnixNano())
	return base64.URLEncoding.EncodeToString([]byte(tok))
}

// Valid returns true if token is a valid, unexpired token returned by Generate.
func Valid(token, key, userID, actionID string) bool {
	return validAtTime(token, key, userID, actionID, time.Now())
}

// validAtTime is like Valid, but it uses now to check if the token is expired.
func validAtTime(token, key, userID, actionID string, now time.Time) bool {
	// Decode the token.
	data, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return false
	}

	// Extract the issue time of the token.
	sep := bytes.LastIndex(data, []byte{':'})
	if sep < 0 {
		return false
	}
	nanos, err := strconv.ParseInt(string(data[sep+1:]), 10, 64)
	if err != nil {
		return false
	}
	issueTime := time.Unix(0, nanos)

	// Check that the token is not expired.
	if now.Sub(issueTime) >= Timeout {
		return false
	}

	// Check that the token is not from the future.
	// Allow 1 minute grace period in case the token is being verified on a
	// machine whose clock is behind the machine that issued the token.
	if issueTime.After(now.Add(1 * time.Minute)) {
		return false
	}

	// Check that the token matches the expected value.
	expected := generateAtTime(key, userID, actionID, issueTime)
	return token == expected
}
