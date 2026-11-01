/*
Copyright 2026 The Perkeep Authors

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

package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/tailscale/tailcat"
)

// accessInfo is a parsed perkeep://access URL, which a server's Client
// access page shows as a QR code. See doc/access-url.md.
type accessInfo struct {
	Name    string `json:"name"`
	Key     string `json:"key"`     // secret
	Tailcat string `json:"tailcat"` // tailcat address, or empty; secret
	URL     string `json:"url"`     // HTTPS base URL, or empty
	CertFP  string `json:"certfp"`  // with URL, the pinned certificate's SHA-256, or empty
}

func parseAccessURL(s string) (accessInfo, error) {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil {
		return accessInfo{}, err
	}
	if u.Scheme != "perkeep" || u.Host != "access" {
		return accessInfo{}, errors.New("not a perkeep://access URL")
	}
	q := u.Query()
	ai := accessInfo{
		Name:    q.Get("name"),
		Key:     q.Get("key"),
		Tailcat: q.Get("tailcat"),
		URL:     q.Get("url"),
		CertFP:  q.Get("certfp"),
	}
	if ai.Key == "" {
		return accessInfo{}, errors.New("access URL has no key")
	}
	if ai.Name == "" {
		return accessInfo{}, errors.New("access URL has no name")
	}
	switch {
	case ai.Tailcat != "" && ai.URL != "":
		return accessInfo{}, errors.New("access URL has both a tailcat address and a URL")
	case ai.Tailcat != "":
		if ai.CertFP != "" {
			return accessInfo{}, errors.New("access URL has a certfp without a url")
		}
		if _, err := tailcat.ParseAddr(tailcat.Addr(ai.Tailcat)); err != nil {
			return accessInfo{}, fmt.Errorf("access URL has a bad tailcat address: %w", err)
		}
	case ai.URL != "":
		bu, err := url.Parse(ai.URL)
		if err != nil || bu.Scheme != "https" || bu.Host == "" {
			return accessInfo{}, errors.New("access URL's url isn't an https URL")
		}
		if ai.CertFP != "" {
			if err := checkCertFP(ai.CertFP); err != nil {
				return accessInfo{}, err
			}
		}
	default:
		return accessInfo{}, errors.New("access URL has neither a tailcat address nor a URL")
	}
	return ai, nil
}

// checkCertFP checks that fp is a certificate fingerprint: a SHA-256 as
// 64 lowercase hex digits.
func checkCertFP(fp string) error {
	if b, err := hex.DecodeString(fp); err != nil || len(b) != 32 || strings.ToLower(fp) != fp {
		return errors.New("certfp isn't 64 lowercase hex digits")
	}
	return nil
}

func (s *server) serveParseAccessURL(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	ai, err := parseAccessURL(req.URL)
	if err != nil {
		writeError(w, errorf(http.StatusBadRequest, err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, ai)
}
