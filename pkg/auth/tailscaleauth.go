/*
Copyright 2022 The Perkeep Authors

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

package auth

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"

	"tailscale.com/client/tailscale"
)

func newTailscaleAuth(arg string) (AuthMode, error) {
	if arg == "full-access-to-tailnet" {
		return &tailscaleAuth{any: true}, nil
	}
	if strings.Contains(arg, "@") {
		lc := &tailscale.LocalClient{}
		return &tailscaleAuth{lc: lc, anyForUser: arg}, nil
	}
	// TODO(bradfitz): use grants: https://tailscale.com/blog/acl-grants
	return nil, errors.New("unknown tailscale auth mode")
}

type tailscaleAuth struct {
	any bool // whether all access is permitted to anybody in the tailnet

	lc         *tailscale.LocalClient
	anyForUser string // if non-empty, the user for whom any access is permitted
}

func (ta *tailscaleAuth) AllowedAccess(req *http.Request) Operation {
	// AddAuthHeader inserts in req the credentials needed
	// for a client to authenticate.
	// TODO: eventially use req.RemoteAddr to talk to Tailscale LocalAPI WhoIs method
	// and check caps.
	if ta.any {
		return OpAll
	}
	if ta.anyForUser != "" {
		res, err := ta.lc.WhoIs(req.Context(), req.RemoteAddr)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				log.Printf("tailscale: WhoIs(%q) = %v", req.RemoteAddr, err)
			}
			return 0
		}
		if res.UserProfile.LoginName == ta.anyForUser {
			return OpAll
		}
		log.Printf("access denied to Tailscale user %q; only %q is allowed", res.UserProfile.LoginName, ta.anyForUser)
		return 0
	}
	return 0
}

func (*tailscaleAuth) AddAuthHeader(req *http.Request) {
	// Nothing
}
