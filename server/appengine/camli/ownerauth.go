// +build appengine

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

package appengine

import (
	"net/http"

	"camlistore.org/pkg/auth"
	"camlistore.org/pkg/httputil"

	"appengine"
	"appengine/user"
)

func init() {
	auth.RegisterAuth("appengine_app_owner", newOwnerAuth)
}

type ownerAuth struct {
	fallback auth.AuthMode
}

var _ auth.UnauthorizedSender = (*ownerAuth)(nil)

func newOwnerAuth(arg string) (auth.AuthMode, error) {
	m := &ownerAuth{}
	if arg != "" {
		f, err := auth.FromConfig(arg)
		if err != nil {
			return nil, err
		}
		m.fallback = f
	}
	return m, nil
}

func (o *ownerAuth) AllowedAccess(req *http.Request) auth.Operation {
	c := appengine.NewContext(req)
	if user.IsAdmin(c) {
		return auth.OpAll
	}
	if o.fallback != nil {
		return o.fallback.AllowedAccess(req)
	}
	return 0
}

func (o *ownerAuth) SendUnauthorized(rw http.ResponseWriter, req *http.Request) bool {
	if !httputil.IsGet(req) {
		return false
	}
	c := appengine.NewContext(req)
	loginURL, err := user.LoginURL(c, req.URL.String())
	if err != nil {
		c.Errorf("Fetching LoginURL: %v", err)
		return false
	}
	http.Redirect(rw, req, loginURL, http.StatusFound)
	return true
}

func (o *ownerAuth) AddAuthHeader(req *http.Request) {
	// TODO(bradfitz): split the auth interface into a server part
	// and a client part.
	panic("Not applicable. should not be called.")
}
