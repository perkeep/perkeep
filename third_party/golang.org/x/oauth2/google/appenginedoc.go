// Copyright 2015 The oauth2 Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build !appengine,!appenginevm

package google

import "camlistore.org/third_party/golang.org/x/oauth2"

// AppEngineTokenSource returns a token source that fetches tokens
// issued to the current App Engine application's service account.
// If you are implementing a 3-legged OAuth 2.0 flow on App Engine
// that involves user accounts, see oauth2.Config instead.
//
// You are required to provide a valid appengine.Context as context.
func AppEngineTokenSource(ctx oauth2.Context, scope ...string) oauth2.TokenSource {
	panic("You should only use an AppEngineTokenSource in an App Engine application.")
}
