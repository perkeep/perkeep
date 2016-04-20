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

package xsrftoken

import (
	"encoding/base64"
	"testing"
	"time"
)

const (
	key      = "quay"
	userID   = "12345678"
	actionID = "POST /form"
)

var (
	now              = time.Now()
	oneMinuteFromNow = now.Add(1 * time.Minute)
)

func TestValidToken(t *testing.T) {
	tok := generateAtTime(key, userID, actionID, now)
	if !validAtTime(tok, key, userID, actionID, oneMinuteFromNow) {
		t.Error("One second later: Expected token to be valid")
	}
	if !validAtTime(tok, key, userID, actionID, now.Add(Timeout-1*time.Nanosecond)) {
		t.Error("Just before timeout: Expected token to be valid")
	}
	if !validAtTime(tok, key, userID, actionID, now.Add(-1*time.Minute)) {
		t.Error("One minute in the past: Expected token to be valid")
	}
}

// TestSeparatorReplacement tests that separators are being correctly substituted
func TestSeparatorReplacement(t *testing.T) {
	tok := generateAtTime("foo:bar", "baz", "wah", now)
	tok2 := generateAtTime("foo", "bar:baz", "wah", now)
	if tok == tok2 {
		t.Errorf("Expected generated tokens to be different")
	}
}

func TestInvalidToken(t *testing.T) {
	invalidTokenTests := []struct {
		name, key, userID, actionID string
		t                           time.Time
	}{
		{"Bad key", "foobar", userID, actionID, oneMinuteFromNow},
		{"Bad userID", key, "foobar", actionID, oneMinuteFromNow},
		{"Bad actionID", key, userID, "foobar", oneMinuteFromNow},
		{"Expired", key, userID, actionID, now.Add(Timeout)},
		{"More than 1 minute from the future", key, userID, actionID, now.Add(-1*time.Nanosecond - 1*time.Minute)},
	}

	tok := generateAtTime(key, userID, actionID, now)
	for _, itt := range invalidTokenTests {
		if validAtTime(tok, itt.key, itt.userID, itt.actionID, itt.t) {
			t.Errorf("%v: Expected token to be invalid", itt.name)
		}
	}
}

// TestValidateBadData primarily tests that no unexpected panics are triggered
// during parsing
func TestValidateBadData(t *testing.T) {
	badDataTests := []struct {
		name, tok string
	}{
		{"Invalid Base64", "ASDab24(@)$*=="},
		{"No delimiter", base64.URLEncoding.EncodeToString([]byte("foobar12345678"))},
		{"Invalid time", base64.URLEncoding.EncodeToString([]byte("foobar:foobar"))},
	}

	for _, bdt := range badDataTests {
		if validAtTime(bdt.tok, key, userID, actionID, oneMinuteFromNow) {
			t.Errorf("%v: Expected token to be invalid", bdt.name)
		}
	}
}
