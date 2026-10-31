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
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const ascBaseURL = "https://api.appstoreconnect.apple.com"

// ascClient is a minimal App Store Connect API client covering the
// provisioning endpoints iossign needs.
type ascClient struct {
	keyID    string
	issuerID string
	key      *ecdsa.PrivateKey
	baseURL  string
	hc       *http.Client
}

func newASCClient(keyID, issuerID, keyPath string) (*ascClient, error) {
	pemBytes, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	key, err := parseP8(pemBytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", keyPath, err)
	}
	return &ascClient{
		keyID:    keyID,
		issuerID: issuerID,
		key:      key,
		baseURL:  ascBaseURL,
		hc:       http.DefaultClient,
	}, nil
}

// parseP8 parses an App Store Connect API private key (.p8 file).
func parseP8(pemBytes []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("no PEM data found")
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	ek, ok := k.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("key is %T, not ECDSA", k)
	}
	return ek, nil
}

// token returns a signed ES256 JWT for the API, valid for 10 minutes
// from now.
func (c *ascClient) token(now time.Time) (string, error) {
	enc := base64.RawURLEncoding
	header, _ := json.Marshal(map[string]string{"alg": "ES256", "kid": c.keyID, "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{
		"iss": c.issuerID,
		"iat": now.Unix(),
		"exp": now.Add(10 * time.Minute).Unix(),
		"aud": "appstoreconnect-v1",
	})
	signingInput := enc.EncodeToString(header) + "." + enc.EncodeToString(claims)
	h := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, c.key, h[:])
	if err != nil {
		return "", err
	}
	// JWS wants the raw fixed-width r||s, not ASN.1.
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return signingInput + "." + enc.EncodeToString(sig), nil
}

// resource is a JSON:API resource object.
type resource struct {
	Type          string                  `json:"type"`
	ID            string                  `json:"id,omitempty"`
	Attributes    map[string]any          `json:"attributes,omitempty"`
	Relationships map[string]relationship `json:"relationships,omitempty"`
}

type relationship struct {
	Data any `json:"data"` // ref or []ref
}

type ref struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

func (r resource) attr(name string) string {
	s, _ := r.Attributes[name].(string)
	return s
}

// do makes an API request. If in is non-nil it's sent as the JSON:API
// "data" member. Responses' "data" member is decoded into out, if non-nil.
func (c *ascClient) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		j, err := json.Marshal(map[string]any{"data": in})
		if err != nil {
			return err
		}
		body = bytes.NewReader(j)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	tok, err := c.token(time.Now())
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	resBody, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return err
	}
	if res.StatusCode/100 != 2 {
		return fmt.Errorf("%s %s: %s: %s", method, path, res.Status, apiErrorText(resBody))
	}
	if out == nil || len(resBody) == 0 {
		return nil
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(resBody, &env); err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	return json.Unmarshal(env.Data, out)
}

func apiErrorText(body []byte) string {
	var e struct {
		Errors []struct {
			Title  string `json:"title"`
			Detail string `json:"detail"`
		} `json:"errors"`
	}
	if json.Unmarshal(body, &e) != nil || len(e.Errors) == 0 {
		return string(body)
	}
	var msgs []string
	for _, x := range e.Errors {
		msgs = append(msgs, x.Title+": "+x.Detail)
	}
	return strings.Join(msgs, "; ")
}

func (c *ascClient) list(ctx context.Context, path string, query url.Values) ([]resource, error) {
	query.Set("limit", "200")
	var rs []resource
	err := c.do(ctx, "GET", path+"?"+query.Encode(), nil, &rs)
	return rs, err
}

// ensureBundleID returns the API ID of the bundle ID identifier,
// registering it if needed.
func (c *ascClient) ensureBundleID(ctx context.Context, identifier string) (string, error) {
	rs, err := c.list(ctx, "/v1/bundleIds", url.Values{"filter[identifier]": {identifier}})
	if err != nil {
		return "", err
	}
	for _, r := range rs {
		// The filter matches prefixes, so check for an exact match.
		if r.attr("identifier") == identifier {
			return r.ID, nil
		}
	}
	var r resource
	err = c.do(ctx, "POST", "/v1/bundleIds", resource{
		Type: "bundleIds",
		Attributes: map[string]any{
			"identifier": identifier,
			"name":       "Perkeep " + identifier,
			"platform":   "IOS",
		},
	}, &r)
	if err != nil {
		return "", err
	}
	fmt.Printf("Registered bundle ID %s\n", identifier)
	return r.ID, nil
}

// ensureDevice returns the API ID of the device with the given UDID,
// registering it if needed.
func (c *ascClient) ensureDevice(ctx context.Context, udid, name string) (string, error) {
	rs, err := c.list(ctx, "/v1/devices", url.Values{"filter[udid]": {udid}})
	if err != nil {
		return "", err
	}
	for _, r := range rs {
		if r.attr("status") == "ENABLED" {
			return r.ID, nil
		}
	}
	if len(rs) > 0 {
		return "", fmt.Errorf("device %s is registered but disabled; re-enable it at developer.apple.com", udid)
	}
	var r resource
	err = c.do(ctx, "POST", "/v1/devices", resource{
		Type: "devices",
		Attributes: map[string]any{
			"name":     name,
			"udid":     udid,
			"platform": "IOS",
		},
	}, &r)
	if err != nil {
		return "", err
	}
	fmt.Printf("Registered device %q (%s)\n", name, udid)
	return r.ID, nil
}

// apiCert is a signing certificate known to App Store Connect.
type apiCert struct {
	ID   string
	DER  []byte
	Name string
}

func (c *ascClient) certificates(ctx context.Context, certType string) ([]apiCert, error) {
	rs, err := c.list(ctx, "/v1/certificates", url.Values{"filter[certificateType]": {certType}})
	if err != nil {
		return nil, err
	}
	var certs []apiCert
	for _, r := range rs {
		der, err := base64.StdEncoding.DecodeString(r.attr("certificateContent"))
		if err != nil {
			return nil, fmt.Errorf("certificate %s: %w", r.ID, err)
		}
		certs = append(certs, apiCert{ID: r.ID, DER: der, Name: r.attr("name")})
	}
	return certs, nil
}

// createCertificate submits a PEM-encoded CSR and returns the new
// certificate.
func (c *ascClient) createCertificate(ctx context.Context, certType string, csrPEM []byte) (apiCert, error) {
	var r resource
	err := c.do(ctx, "POST", "/v1/certificates", resource{
		Type: "certificates",
		Attributes: map[string]any{
			"certificateType": certType,
			"csrContent":      string(csrPEM),
		},
	}, &r)
	if err != nil {
		return apiCert{}, err
	}
	der, err := base64.StdEncoding.DecodeString(r.attr("certificateContent"))
	if err != nil {
		return apiCert{}, err
	}
	return apiCert{ID: r.ID, DER: der, Name: r.attr("name")}, nil
}

// ensureProfile returns the contents of an active provisioning profile
// named name of the given type for bundleID, covering exactly certID
// and deviceIDs. A same-named profile with different contents is
// replaced, since profiles can't be edited.
func (c *ascClient) ensureProfile(ctx context.Context, name, profileType, bundleID, certID string, deviceIDs []string) ([]byte, error) {
	rs, err := c.list(ctx, "/v1/profiles", url.Values{"filter[name]": {name}})
	if err != nil {
		return nil, err
	}
	for _, r := range rs {
		if r.attr("name") != name {
			continue
		}
		ok, err := c.profileMatches(ctx, r, certID, deviceIDs)
		if err != nil {
			return nil, err
		}
		if ok {
			return base64.StdEncoding.DecodeString(r.attr("profileContent"))
		}
		fmt.Printf("Replacing out-of-date provisioning profile %q\n", name)
		if err := c.do(ctx, "DELETE", "/v1/profiles/"+r.ID, nil, nil); err != nil {
			return nil, err
		}
	}

	rels := map[string]relationship{
		"bundleId":     {Data: ref{"bundleIds", bundleID}},
		"certificates": {Data: []ref{{"certificates", certID}}},
	}
	if len(deviceIDs) > 0 {
		var devs []ref
		for _, id := range deviceIDs {
			devs = append(devs, ref{"devices", id})
		}
		rels["devices"] = relationship{Data: devs}
	}
	var r resource
	err = c.do(ctx, "POST", "/v1/profiles", resource{
		Type:          "profiles",
		Attributes:    map[string]any{"name": name, "profileType": profileType},
		Relationships: rels,
	}, &r)
	if err != nil {
		return nil, err
	}
	fmt.Printf("Created provisioning profile %q\n", name)
	return base64.StdEncoding.DecodeString(r.attr("profileContent"))
}

func (c *ascClient) profileMatches(ctx context.Context, p resource, certID string, deviceIDs []string) (bool, error) {
	if p.attr("profileState") != "ACTIVE" {
		return false, nil
	}
	ids := func(rel string) (map[string]bool, error) {
		var refs []ref
		if err := c.do(ctx, "GET", "/v1/profiles/"+p.ID+"/relationships/"+rel+"?limit=200", nil, &refs); err != nil {
			return nil, err
		}
		m := map[string]bool{}
		for _, r := range refs {
			m[r.ID] = true
		}
		return m, nil
	}
	certs, err := ids("certificates")
	if err != nil {
		return false, err
	}
	if len(certs) != 1 || !certs[certID] {
		return false, nil
	}
	if len(deviceIDs) == 0 {
		return true, nil
	}
	devs, err := ids("devices")
	if err != nil {
		return false, err
	}
	if len(devs) != len(deviceIDs) {
		return false, nil
	}
	for _, id := range deviceIDs {
		if !devs[id] {
			return false, nil
		}
	}
	return true, nil
}
