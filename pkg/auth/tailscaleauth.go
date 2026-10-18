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

	"tailscale.com/util/ctxkey"
)

// TailscaleWhoIsCtxKey holds the identity lookup for requests accepted by a
// Tailscale listener. Only trusted server code should set a non-nil function;
// its presence establishes that the request came over Tailscale. The function
// returns the login name of the user at the given request RemoteAddr (IP:port).
// Full-tailnet access uses its presence without calling the function.
//
// Keeping the lookup abstract lets clients use package auth without linking a
// Tailscale node.
var TailscaleWhoIsCtxKey = ctxkey.New("tailscale-whois", (func(ctx context.Context, remoteAddr string) (loginName string, err error))(nil))

func newTailscaleAuth(arg string) (AuthMode, error) {
	if arg == "full-access-to-tailnet" {
		return &tailscaleAuth{any: true}, nil
	}
	if strings.Contains(arg, "@") {
		return &tailscaleAuth{anyForUser: arg}, nil
	}
	// TODO(bradfitz): use grants: https://tailscale.com/blog/acl-grants
	return nil, errors.New("unknown tailscale auth mode")
}

type tailscaleAuth struct {
	any bool // whether all access is permitted to anybody in the tailnet

	anyForUser string // if non-empty, the user for whom any access is permitted
}

func (ta *tailscaleAuth) AllowedAccess(req *http.Request) Operation {
	whoIs := TailscaleWhoIsCtxKey.Value(req.Context())
	if whoIs == nil {
		return 0
	}

	if ta.any {
		return OpAll
	}
	if ta.anyForUser != "" {
		loginName, err := whoIs(req.Context(), req.RemoteAddr)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				log.Printf("tailscale: WhoIs(%q) = %v", req.RemoteAddr, err)
			}
			return 0
		}
		if loginName == ta.anyForUser {
			return OpAll
		}
		log.Printf("access denied to Tailscale user %q; only %q is allowed", loginName, ta.anyForUser)
		return 0
	}
	return 0
}

func (*tailscaleAuth) AddAuthHeader(req *http.Request) {
	// Nothing
}
