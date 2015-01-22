// Copyright 2015 The oauth2 Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package google

import "testing"

func TestSDKConfig(t *testing.T) {
	unixHomeDir = "testdata"
	tests := []struct {
		account     string
		accessToken string
		err         bool
	}{
		{"", "bar_access_token", false},
		{"foo@example.com", "foo_access_token", false},
		{"bar@example.com", "bar_access_token", false},
	}
	for _, tt := range tests {
		c, err := NewSDKConfig(tt.account)
		if (err != nil) != tt.err {
			if !tt.err {
				t.Errorf("expected no error, got error: %v", tt.err, err)
			} else {
				t.Errorf("execcted error, got none")
			}
			continue
		}
		tok := c.initialToken
		if tok == nil {
			t.Errorf("expected token %q, got: nil", tt.accessToken)
			continue
		}
		if tok.AccessToken != tt.accessToken {
			t.Errorf("expected token %q, got: %q", tt.accessToken, tok.AccessToken)
		}
	}
}
