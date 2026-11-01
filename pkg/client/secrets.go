/*
Copyright 2026 The Perkeep Authors.

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

package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"unicode/utf8"

	"perkeep.org/pkg/types/camtypes"
)

// ErrNoSecretRoot means the server does not advertise owner secret access.
var ErrNoSecretRoot = errors.New("client: server does not support secret access with these credentials")

// SecretPutOptions controls compare-and-swap and migration warning removal.
type SecretPutOptions struct {
	// ExpectedRevision is -1 for an unconditional write, zero to require
	// absence, or the current positive revision. Zero permits recreating
	// a deleted name; recreation continues its revision sequence.
	ExpectedRevision int64 `json:"expectedRevision"`

	// ClearMigration explicitly acknowledges that the credential's historical
	// exposure has been addressed and clears MigratedFromPermanode.
	// Ordinary writes preserve the warning.
	ClearMigration bool `json:"clearMigration,omitempty"`
}

// SecretHTTPError is a secret API error. It never includes the request URL,
// server's response body, or any secret value.
type SecretHTTPError struct {
	// StatusCode is the unsuccessful HTTP response status.
	StatusCode int
}

func (e *SecretHTTPError) Error() string {
	return fmt.Sprintf("client: secret request failed (HTTP %d)", e.StatusCode)
}

// SecretRoot returns the owner-only secret handler URL, if advertised.
func (c *Client) SecretRoot() (string, error) {
	if err := c.condDiscovery(); err != nil {
		return "", err
	}
	if c.secretRoot == "" {
		return "", ErrNoSecretRoot
	}
	return c.secretRoot, nil
}

// ListSecrets returns metadata only, never secret values.
func (c *Client) ListSecrets(ctx context.Context) ([]camtypes.SecretMetadata, error) {
	var out []camtypes.SecretMetadata
	err := c.secretRequest(ctx, http.MethodGet, "", nil, &out)
	return out, err
}

// GetSecret explicitly retrieves a secret value and its migration metadata.
func (c *Client) GetSecret(ctx context.Context, name string) (camtypes.SecretEntry, error) {
	var out camtypes.SecretEntry
	if name == "" {
		return out, errors.New("client: secret name required")
	}
	err := c.secretRequest(ctx, http.MethodGet, name, nil, &out)
	return out, err
}

// PutSecret stores a value. Ordinary writes retain migration warnings unless
// ClearMigration explicitly confirms rotation. Values must be valid UTF-8 and
// are sent only in JSON.
func (c *Client) PutSecret(ctx context.Context, name, value string, opts SecretPutOptions) (camtypes.SecretMetadata, error) {
	var out camtypes.SecretMetadata
	if !utf8.ValidString(value) {
		return out, errors.New("client: secret value must be valid UTF-8")
	}
	body := struct {
		Value string `json:"value"`
		SecretPutOptions
	}{value, opts}
	err := c.secretRequest(ctx, http.MethodPut, name, body, &out)
	return out, err
}

// DeleteSecret deletes a value with compare-and-swap (-1 means unconditional).
func (c *Client) DeleteSecret(ctx context.Context, name string, expectedRevision int64) error {
	return c.secretRequest(ctx, http.MethodDelete, name, struct {
		ExpectedRevision int64 `json:"expectedRevision"`
	}{expectedRevision}, nil)
}

func (c *Client) secretRequest(ctx context.Context, method, name string, body, out any) error {
	root, err := c.SecretRoot()
	if err != nil {
		return err
	}
	endpoint := root
	if name != "" {
		endpoint += "value?" + url.Values{"name": {name}}.Encode()
	} else if method != http.MethodGet || out == nil {
		return errors.New("client: secret name required")
	}
	var b io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return errors.New("client: could not encode secret request")
		}
		b = bytes.NewReader(data)
	}
	// Use configured credentials, NOT the OpAll token in discovery.
	req := c.newRequest(ctx, method, endpoint, b)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// Never forward a secret body on a redirect, including to the same host.
	hc := *c.httpClient
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c.httpGate.Start()
	res, err := hc.Do(req)
	c.httpGate.Done()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("client: secret request transport failed")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return &SecretHTTPError{res.StatusCode}
	}
	if out == nil {
		return nil
	}
	// Bound responses even from a buggy server. A JSON string may expand to six
	// times its raw size, and a metadata listing may contain many entries.
	const maxResponse = 16 << 20
	data, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
	if err != nil || len(data) > maxResponse {
		return errors.New("client: invalid secret response")
	}
	if err := json.Unmarshal(data, out); err != nil {
		return errors.New("client: invalid secret response")
	}
	return nil
}
