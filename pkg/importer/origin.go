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

package importer

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// importerMutationOrigin protects ambient-auth form endpoints. Non-browser
// clients may omit Origin. Behind a TLS-terminating proxy only the configured
// public BaseURL is trusted, never caller-supplied Forwarded headers.
func importerMutationOrigin(r *http.Request, baseURL string) error {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return errors.New("cross-origin importer request rejected")
	}
	origins := r.Header.Values("Origin")
	if len(origins) == 0 {
		return nil
	}
	if len(origins) != 1 {
		return errors.New("invalid Origin")
	}
	u, err := url.Parse(origins[0])
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return errors.New("cross-origin importer request rejected")
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if u.Scheme == scheme && strings.EqualFold(u.Host, r.Host) {
		return nil
	}
	public, err := url.Parse(baseURL)
	if err == nil && (public.Scheme == "http" || public.Scheme == "https") && public.Host != "" && public.Scheme == u.Scheme && strings.EqualFold(public.Host, u.Host) {
		return nil
	}
	return errors.New("cross-origin importer request rejected")
}

func (h *Host) allowMutation(w http.ResponseWriter, r *http.Request) bool {
	if err := importerMutationOrigin(r, h.baseURL); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return false
	}
	return true
}
