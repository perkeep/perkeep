// Copyright 2010 Gary Burd
//
// Licensed under the Apache License, Version 2.0 (the "License"): you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
// WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
// License for the specific language governing permissions and limitations
// under the License.

// Package oauth is consumer interface for OAuth 1.0, OAuth 1.0a and RFC 5849.
//
// Redirection-based Authorization
//
// This section outlines how to use the oauth package in redirection-based
// authorization (http://tools.ietf.org/html/rfc5849#section-2).
//
// Step 1: Create a Client using credentials and URIs provided by the server.
// The Client can be initialized once at application startup and stored in a
// package-level variable.
//
// Step 2: Request temporary credentials using the Client
// RequestTemporaryCredentials method. The callbackURL parameter is the URL of
// the callback handler in step 4. Save the returned credential secret so that
// it can be later found using credential token as a key. The secret can be
// stored in a database keyed by the token. Another option is to store the
// token and secret in session storage or a cookie.
//
// Step 3: Redirect the user to URL returned from AuthorizationURL method. The
// AuthorizationURL method uses the temporary credentials from step 2 and other
// parameters as specified by the server.
//
// Step 4: The server redirects back to the callback URL specified in step 2
// with the temporary token and a verifier. Use the temporary token to find the
// temporary secret saved in step 2. Using the temporary token, temporary
// secret and verifier, request token credentials using the client RequestToken
// method. Save the returned credentials for later use in the application.
//
// Signing Requests
//
// The Client type has two low-level methods for signing requests, SignForm and
// AuthorizationHeader.
//
// The SignForm method adds an OAuth signature to a form. The application makes
// an authenticated request by encoding the modified form to the query string
// or request body.
//
// The AuthorizationHeader method returns an Authorization header value with
// the OAuth signature. The application makes an authenticated request by
// adding the Authorization header to the request. The AuthorizationHeader
// method is the only way to correctly sign a request if the application sets
// the URL Opaque field when making a request.
//
// The Get and Post methods sign and invoke a request using the supplied
// net/http Client. These methods are easy to use, but not as flexible as
// constructing a request using one of the low-level methods.
package oauth

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// noscape[b] is true if b should not be escaped per section 3.6 of the RFC.
var noEscape = [256]bool{
	'A': true, true, true, true, true, true, true, true, true, true, true, true, true, true, true, true, true, true, true, true, true, true, true, true, true, true,
	'a': true, true, true, true, true, true, true, true, true, true, true, true, true, true, true, true, true, true, true, true, true, true, true, true, true, true,
	'0': true, true, true, true, true, true, true, true, true, true,
	'-': true,
	'.': true,
	'_': true,
	'~': true,
}

// encode encodes string per section 3.6 of the RFC. If double is true, then
// the encoding is applied twice.
func encode(s string, double bool) []byte {
	// Compute size of result.
	m := 3
	if double {
		m = 5
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if noEscape[s[i]] {
			n += 1
		} else {
			n += m
		}
	}

	p := make([]byte, n)

	// Encode it.
	j := 0
	for i := 0; i < len(s); i++ {
		b := s[i]
		if noEscape[b] {
			p[j] = b
			j += 1
		} else if double {
			p[j] = '%'
			p[j+1] = '2'
			p[j+2] = '5'
			p[j+3] = "0123456789ABCDEF"[b>>4]
			p[j+4] = "0123456789ABCDEF"[b&15]
			j += 5
		} else {
			p[j] = '%'
			p[j+1] = "0123456789ABCDEF"[b>>4]
			p[j+2] = "0123456789ABCDEF"[b&15]
			j += 3
		}
	}
	return p
}

type keyValue struct{ key, value []byte }

type byKeyValue []keyValue

func (p byKeyValue) Len() int      { return len(p) }
func (p byKeyValue) Swap(i, j int) { p[i], p[j] = p[j], p[i] }
func (p byKeyValue) Less(i, j int) bool {
	sgn := bytes.Compare(p[i].key, p[j].key)
	if sgn == 0 {
		sgn = bytes.Compare(p[i].value, p[j].value)
	}
	return sgn < 0
}

func (p byKeyValue) appendValues(values url.Values) byKeyValue {
	for k, vs := range values {
		k := encode(k, true)
		for _, v := range vs {
			v := encode(v, true)
			p = append(p, keyValue{k, v})
		}
	}
	return p
}

// writeBaseString writes method, url, and params to w using the OAuth signature
// base string computation described in section 3.4.1 of the RFC.
func writeBaseString(w io.Writer, method string, u *url.URL, form url.Values, oauthParams map[string]string) {
	// Method
	w.Write(encode(strings.ToUpper(method), false))
	w.Write([]byte{'&'})

	// URL
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Host)

	uNoQuery := *u
	uNoQuery.RawQuery = ""
	path := uNoQuery.RequestURI()

	switch {
	case scheme == "http" && strings.HasSuffix(host, ":80"):
		host = host[:len(host)-len(":80")]
	case scheme == "https" && strings.HasSuffix(host, ":443"):
		host = host[:len(host)-len(":443")]
	}

	w.Write(encode(scheme, false))
	w.Write(encode("://", false))
	w.Write(encode(host, false))
	w.Write(encode(path, false))
	w.Write([]byte{'&'})

	// Create sorted slice of encoded parameters. Parameter keys and values are
	// double encoded in a single step. This is safe because double encoding
	// does not change the sort order.
	queryParams := u.Query()
	p := make(byKeyValue, 0, len(form)+len(queryParams)+len(oauthParams))
	p = p.appendValues(form)
	p = p.appendValues(queryParams)
	for k, v := range oauthParams {
		p = append(p, keyValue{encode(k, true), encode(v, true)})
	}
	sort.Sort(p)

	// Write the parameters.
	encodedAmp := encode("&", false)
	encodedEqual := encode("=", false)
	sep := false
	for _, kv := range p {
		if sep {
			w.Write(encodedAmp)
		} else {
			sep = true
		}
		w.Write(kv.key)
		w.Write(encodedEqual)
		w.Write(kv.value)
	}
}

var (
	nonceLock    sync.Mutex
	nonceCounter uint64
)

// nonce returns a unique string.
func nonce() string {
	nonceLock.Lock()
	defer nonceLock.Unlock()
	if nonceCounter == 0 {
		binary.Read(rand.Reader, binary.BigEndian, &nonceCounter)
	}
	result := strconv.FormatUint(nonceCounter, 16)
	nonceCounter += 1
	return result
}

// oauthParams returns the OAuth request parameters for the given credentials,
// method, URL and application params. See
// http://tools.ietf.org/html/rfc5849#section-3.4 for more information about
// signatures.
func oauthParams(clientCredentials *Credentials, credentials *Credentials, method string, u *url.URL, form url.Values) map[string]string {
	oauthParams := map[string]string{
		"oauth_consumer_key":     clientCredentials.Token,
		"oauth_signature_method": "HMAC-SHA1",
		"oauth_timestamp":        strconv.FormatInt(time.Now().Unix(), 10),
		"oauth_version":          "1.0",
		"oauth_nonce":            nonce(),
	}
	if credentials != nil {
		oauthParams["oauth_token"] = credentials.Token
	}
	if testingNonce != "" {
		oauthParams["oauth_nonce"] = testingNonce
	}
	if testingTimestamp != "" {
		oauthParams["oauth_timestamp"] = testingTimestamp
	}

	var key bytes.Buffer
	key.Write(encode(clientCredentials.Secret, false))
	key.WriteByte('&')
	if credentials != nil {
		key.Write(encode(credentials.Secret, false))
	}

	h := hmac.New(sha1.New, key.Bytes())
	writeBaseString(h, method, u, form, oauthParams)
	sum := h.Sum(nil)

	encodedSum := make([]byte, base64.StdEncoding.EncodedLen(len(sum)))
	base64.StdEncoding.Encode(encodedSum, sum)

	oauthParams["oauth_signature"] = string(encodedSum)
	return oauthParams
}

// Client represents an OAuth client.
type Client struct {
	Credentials                   Credentials
	TemporaryCredentialRequestURI string // Also known as request token URL.
	ResourceOwnerAuthorizationURI string // Also known as authorization URL.
	TokenRequestURI               string // Also known as access token URL.
}

// Credentials represents client, temporary and token credentials.
type Credentials struct {
	Token  string // Also known as consumer key or access token.
	Secret string // Also known as consumer secret or access token secret.
}

var (
	testingTimestamp string
	testingNonce     string
)

// SignForm adds an OAuth signature to form. The urlStr argument must not
// include a query string.
//
// See http://tools.ietf.org/html/rfc5849#section-3.5.2 for
// information about transmitting OAuth parameters in a request body and
// http://tools.ietf.org/html/rfc5849#section-3.5.2 for information about
// transmitting OAuth parameters in a query string.
func (c *Client) SignForm(credentials *Credentials, method, urlStr string, form url.Values) error {
	u, err := url.Parse(urlStr)
	switch {
	case err != nil:
		return err
	case u.RawQuery != "":
		return errors.New("oauth: urlStr argument to SignForm must not include a query string")
	}
	for k, v := range oauthParams(&c.Credentials, credentials, method, u, form) {
		form.Set(k, v)
	}
	return nil
}

// SignParam is deprecated. Use SignForm instead.
func (c *Client) SignParam(credentials *Credentials, method, urlStr string, params url.Values) {
	u, _ := url.Parse(urlStr)
	u.RawQuery = ""
	for k, v := range oauthParams(&c.Credentials, credentials, method, u, params) {
		params.Set(k, v)
	}
}

// AuthorizationHeader returns the HTTP authorization header value for given
// method, URL and parameters.
//
// See http://tools.ietf.org/html/rfc5849#section-3.5.1 for information about
// transmitting OAuth parameters in an HTTP request header.
func (c *Client) AuthorizationHeader(credentials *Credentials, method string, u *url.URL, params url.Values) string {
	p := oauthParams(&c.Credentials, credentials, method, u, params)
	var buf bytes.Buffer
	buf.WriteString(`OAuth oauth_consumer_key="`)
	buf.Write(encode(p["oauth_consumer_key"], false))
	buf.WriteString(`", oauth_nonce="`)
	buf.Write(encode(p["oauth_nonce"], false))
	buf.WriteString(`", oauth_signature="`)
	buf.Write(encode(p["oauth_signature"], false))
	buf.WriteString(`", oauth_signature_method="HMAC-SHA1", oauth_timestamp="`)
	buf.Write(encode(p["oauth_timestamp"], false))
	if t, ok := p["oauth_token"]; ok {
		buf.WriteString(`", oauth_token="`)
		buf.Write(encode(t, false))
	}
	buf.WriteString(`", oauth_version="1.0"`)
	return buf.String()
}

// Get issues a GET to the specified URL with form added as a query string.
func (c *Client) Get(client *http.Client, credentials *Credentials, urlStr string, form url.Values) (*http.Response, error) {
	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		return nil, err
	}
	if req.URL.RawQuery != "" {
		return nil, errors.New("oauth: url must not contain a query string")
	}
	req.Header.Set("Authorization", c.AuthorizationHeader(credentials, "GET", req.URL, form))
	req.URL.RawQuery = form.Encode()
	return client.Do(req)
}

// Post issues a POST with the specified form.
func (c *Client) Post(client *http.Client, credentials *Credentials, urlStr string, form url.Values) (*http.Response, error) {
	req, err := http.NewRequest("POST", urlStr, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", c.AuthorizationHeader(credentials, "POST", req.URL, form))
	return client.Do(req)
}

func (c *Client) request(client *http.Client, credentials *Credentials, urlStr string, params url.Values) (*Credentials, url.Values, error) {
	c.SignParam(credentials, "POST", urlStr, params)
	resp, err := client.PostForm(urlStr, params)
	if err != nil {
		return nil, nil, err
	}
	p, err := ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		return nil, nil, fmt.Errorf("OAuth server status %d, %s", resp.StatusCode, string(p))
	}
	m, err := url.ParseQuery(string(p))
	if err != nil {
		return nil, nil, err
	}
	tokens := m["oauth_token"]
	if len(tokens) == 0 || tokens[0] == "" {
		return nil, nil, errors.New("oauth: token missing from server result")
	}
	secrets := m["oauth_token_secret"]
	if len(secrets) == 0 { // allow "" as a valid secret.
		return nil, nil, errors.New("oauth: secret mssing from server result")
	}
	return &Credentials{Token: tokens[0], Secret: secrets[0]}, m, nil
}

// RequestTemporaryCredentials requests temporary credentials from the server.
// See http://tools.ietf.org/html/rfc5849#section-2.1 for information about
// temporary credentials.
func (c *Client) RequestTemporaryCredentials(client *http.Client, callbackURL string, additionalParams url.Values) (*Credentials, error) {
	params := make(url.Values)
	for k, vs := range additionalParams {
		params[k] = vs
	}
	if callbackURL != "" {
		params.Set("oauth_callback", callbackURL)
	}
	credentials, _, err := c.request(client, nil, c.TemporaryCredentialRequestURI, params)
	return credentials, err
}

// RequestToken requests token credentials from the server. See
// http://tools.ietf.org/html/rfc5849#section-2.3 for information about token
// credentials.
func (c *Client) RequestToken(client *http.Client, temporaryCredentials *Credentials, verifier string) (*Credentials, url.Values, error) {
	params := make(url.Values)
	if verifier != "" {
		params.Set("oauth_verifier", verifier)
	}
	credentials, vals, err := c.request(client, temporaryCredentials, c.TokenRequestURI, params)
	if err != nil {
		return nil, nil, err
	}
	return credentials, vals, nil
}

// AuthorizationURL returns the URL for resource owner authorization. See
// http://tools.ietf.org/html/rfc5849#section-2.2 for information about
// resource owner authorization.
func (c *Client) AuthorizationURL(temporaryCredentials *Credentials, additionalParams url.Values) string {
	params := make(url.Values)
	for k, vs := range additionalParams {
		params[k] = vs
	}
	params.Set("oauth_token", temporaryCredentials.Token)
	return c.ResourceOwnerAuthorizationURI + "?" + params.Encode()
}
