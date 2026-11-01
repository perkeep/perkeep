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
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/tailscale/tailcat"
	"perkeep.org/pkg/types/camtypes"
)

// conn is how to reach a server.
type conn struct {
	Server   string `json:"server"`  // base URL, if not using tailcat
	CertFP   string `json:"certfp"`  // with Server, the pinned certificate's SHA-256, or empty
	Tailcat  string `json:"tailcat"` // tailcat address, or empty; secret
	Username string `json:"username"`
	Password string `json:"password"` // password or access key; secret
}

// tailcatTransport is an HTTP client for one tailcat address.
type tailcatTransport struct {
	tc *tailcat.Client
	hc *http.Client
}

// httpClient returns an HTTP client for c and the server's base URL.
func (s *server) httpClient(c conn) (*http.Client, string, error) {
	if c.Tailcat != "" {
		return s.tailcatHTTPClient(c.Tailcat), "http://perkeep", nil
	}
	f := accountFields{Server: c.Server, CertFP: c.CertFP}
	if err := f.normalize(); err != nil {
		return nil, "", err
	}
	if f.CertFP == "" {
		return http.DefaultClient, f.Server, nil
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = pinnedTLSConfig(f.CertFP)
	return &http.Client{Transport: tr}, f.Server, nil
}

// pinnedTLSConfig returns a TLS config accepting only the certificate
// whose SHA-256 is certFP (in hex), as access URLs' certfp requires
// for servers whose certificates can't be verified the usual way.
func pinnedTLSConfig(certFP string) *tls.Config {
	return &tls.Config{
		// Instead of the usual verification, VerifyConnection checks
		// the pin.
		InsecureSkipVerify: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("server sent no certificate")
			}
			sum := sha256.Sum256(cs.PeerCertificates[0].Raw)
			if got := hex.EncodeToString(sum[:]); got != certFP {
				return fmt.Errorf("server's certificate has fingerprint %s, not the expected %s", got, certFP)
			}
			return nil
		},
	}
}

// tailcatHTTPClient returns the HTTP client for the server at tailcat
// address addr, starting a tailcat client for it if needed. Clients are
// kept for reuse, as starting one takes a while.
func (s *server) tailcatHTTPClient(addr string) *http.Client {
	s.tailcatMu.Lock()
	defer s.tailcatMu.Unlock()
	if t, ok := s.tailcats[addr]; ok {
		return t.hc
	}
	tc := &tailcat.Client{Server: tailcat.Addr(addr)}
	tr := &http.Transport{
		// The server serves plain HTTP on port 80 inside the tunnel,
		// which WireGuard already encrypts and authenticates.
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return tc.DialTCPPort(ctx, 80)
		},
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     90 * time.Second,
	}
	t := &tailcatTransport{tc: tc, hc: &http.Client{Transport: tr}}
	if s.tailcats == nil {
		s.tailcats = map[string]*tailcatTransport{}
	}
	s.tailcats[addr] = t
	return t.hc
}

// checkResult is the result of checking that a server is reachable and
// accepts the credentials.
type checkResult struct {
	OK        bool    `json:"ok"`
	Status    int     `json:"status"`  // HTTP status, or 0 if there was no response
	Message   string  `json:"message"` // for the user
	OwnerName string  `json:"ownerName,omitempty"`
	Seconds   float64 `json:"seconds"` // how long the check took
}

const checkTimeout = 30 * time.Second

// check fetches the server's discovery document, which any valid
// credentials may get.
func (s *server) check(ctx context.Context, c conn) checkResult {
	start := time.Now()
	res := s.checkDiscovery(ctx, c)
	res.Seconds = time.Since(start).Seconds()
	return res
}

func (s *server) checkDiscovery(ctx context.Context, c conn) checkResult {
	hc, base, err := s.httpClient(c)
	if err != nil {
		return checkResult{Message: err.Error()}
	}
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", base+"/", nil)
	if err != nil {
		return checkResult{Message: err.Error()}
	}
	req.Header.Set("Accept", "text/x-camli-configuration")
	if c.Username != "" || c.Password != "" {
		user := c.Username
		if user == "" {
			user = "perkeep" // access keys accept any username
		}
		req.SetBasicAuth(user, c.Password)
	}
	resp, err := hc.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return checkResult{Message: fmt.Sprintf("No response from the server within %v.", checkTimeout)}
		}
		return checkResult{Message: "Couldn't connect: " + err.Error()}
	}
	defer resp.Body.Close()
	res := checkResult{Status: resp.StatusCode}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		res.Message = "The server didn't accept the access key or password."
		return res
	case http.StatusForbidden:
		res.Message = "The server accepted the credentials but doesn't allow this request."
		return res
	default:
		res.Message = "The server responded with " + resp.Status + "."
		return res
	}
	var d camtypes.Discovery
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		res.Message = "Error reading the server's response: " + err.Error()
		return res
	}
	if err := json.Unmarshal(body, &d); err != nil || d.BlobRoot == "" {
		res.Message = "The server responded, but not like a Perkeep server."
		return res
	}
	res.OK = true
	res.OwnerName = d.OwnerName
	res.Message = "Connected."
	return res
}

func (s *server) serveCheck(w http.ResponseWriter, r *http.Request) {
	var c conn
	if err := readJSON(r, &c); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.check(r.Context(), c))
}
