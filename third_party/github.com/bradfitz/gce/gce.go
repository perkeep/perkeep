/*
Copyright 2014 Google & the Go AUTHORS

Go AUTHORS are:
See https://code.google.com/p/go/source/browse/AUTHORS

Licensed under the terms of Go itself:
https://code.google.com/p/go/source/browse/LICENSE
*/

// Package gce provides access to Google Compute Engine (GCE) metadata and
// API service accounts.
//
// Most of this package is a wrapper around the GCE metadata service,
// as documented at https://developers.google.com/compute/docs/metadata.
package gce

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Strings is a list of strings.
type Strings []string

// Contains reports whether v is contained in s.
func (s Strings) Contains(v string) bool {
	for _, sv := range s {
		if v == sv {
			return true
		}
	}
	return false
}

var metaClient = &http.Client{
	Transport: &http.Transport{
		Dial: (&net.Dialer{
			Timeout:   750 * time.Millisecond,
			KeepAlive: 30 * time.Second,
		}).Dial,
		ResponseHeaderTimeout: 750 * time.Millisecond,
	},
}

// MetadataValue returns a value from the metadata service.
// The suffix is appended to "http://metadata/computeMetadata/v1/".
func MetadataValue(suffix string) (string, error) {
	// Using 169.254.169.254 instead of "metadata" here because Go
	// binaries built with the "netgo" tag and without cgo won't
	// know the search suffix for "metadata" is
	// ".google.internal", and this IP address is documented as
	// being stable anyway.
	url := "http://169.254.169.254/computeMetadata/v1/" + suffix
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Metadata-Flavor", "Google")
	res, err := metaClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", fmt.Errorf("status code %d trying to fetch %s", res.StatusCode, url)
	}
	all, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	return string(all), nil
}

func metaValueTrim(suffix string) (s string, err error) {
	s, err = MetadataValue(suffix)
	s = strings.TrimSpace(s)
	return
}

type cachedValue struct {
	k    string
	trim bool
	mu   sync.Mutex
	v    string
}

var (
	proj   = &cachedValue{k: "project/project-id", trim: true}
	projID = &cachedValue{k: "project/numeric-project-id", trim: true}
	instID = &cachedValue{k: "instance/id", trim: true}
)

func (c *cachedValue) get() (v string, err error) {
	defer c.mu.Unlock()
	c.mu.Lock()
	if c.v != "" {
		return c.v, nil
	}
	if c.trim {
		v, err = metaValueTrim(c.k)
	} else {
		v, err = MetadataValue(c.k)
	}
	if err == nil {
		c.v = v
	}
	return
}

var onGCE struct {
	sync.Mutex
	set bool
	v   bool
}

// OnGCE reports whether this process is running on Google Compute Engine.
func OnGCE() bool {
	defer onGCE.Unlock()
	onGCE.Lock()
	if onGCE.set {
		return onGCE.v
	}
	onGCE.set = true

	res, err := metaClient.Get("http://metadata.google.internal")
	if err != nil {
		return false
	}
	onGCE.v = res.Header.Get("Metadata-Flavor") == "Google"
	return onGCE.v
}

// ProjectID returns the current instance's project ID string.
func ProjectID() (string, error) { return proj.get() }

// NumericProjectID returns the current instance's numeric project ID.
func NumericProjectID() (string, error) { return projID.get() }

// InternalIP returns the instance's primary internal IP address.
func InternalIP() (string, error) {
	return metaValueTrim("instance/network-interfaces/0/ip")
}

// ExternalIP returns the instance's primary external (public) IP address.
func ExternalIP() (string, error) {
	return metaValueTrim("instance/network-interfaces/0/access-configs/0/external-ip")
}

// Hostname returns the instance's hostname. This will probably be of
// the form "INSTANCENAME.c.PROJECT.internal" but that isn't
// guaranteed.
//
// TODO: what is this defined to be? Docs say "The host name of the
// instance."
func Hostname() (string, error) {
	return metaValueTrim("network-interfaces/0/ip")
}

// InstanceTags returns the list of user-defined instance tags,
// assigned when initially creating a GCE instance.
func InstanceTags() (Strings, error) {
	var s Strings
	j, err := MetadataValue("instance/tags")
	if err != nil {
		return nil, err
	}
	if err := json.NewDecoder(strings.NewReader(j)).Decode(&s); err != nil {
		return nil, err
	}
	return s, nil
}

// InstanceID returns the current VM's numeric instance ID.
func InstanceID() (string, error) {
	return instID.get()
}

// InstanceAttributes returns the list of user-defined attributes,
// assigned when initially creating a GCE VM instance. The value of an
// attribute can be obtained with InstanceAttributeValue.
func InstanceAttributes() (Strings, error) { return lines("instance/attributes/") }

// ProjectAttributes returns the list of user-defined attributes
// applying to the project as a whole, not just this VM.  The value of
// an attribute can be obtained with ProjectAttributeValue.
func ProjectAttributes() (Strings, error) { return lines("project/attributes/") }

func lines(suffix string) (Strings, error) {
	j, err := MetadataValue(suffix)
	if err != nil {
		return nil, err
	}
	s := strings.Split(strings.TrimSpace(j), "\n")
	for i := range s {
		s[i] = strings.TrimSpace(s[i])
	}
	return Strings(s), nil
}

// InstanceAttributeValue returns the value of the provided VM
// instance attribute.
func InstanceAttributeValue(attr string) (string, error) {
	return MetadataValue("instance/attributes/" + attr)
}

// ProjectAttributeValue returns the value of the provided
// project attribute.
func ProjectAttributeValue(attr string) (string, error) {
	return MetadataValue("project/attributes/" + attr)
}

// Scopes returns the service account scopes for the given account.
// The account may be empty or the string "default" to use the instance's
// main account.
func Scopes(serviceAccount string) (Strings, error) {
	if serviceAccount == "" {
		serviceAccount = "default"
	}
	return lines("instance/service-accounts/" + serviceAccount + "/scopes")
}

// Transport is an HTTP transport that adds authentication headers to
// the request using the default GCE service account and forwards the
// requests to the http package's default transport.
var Transport = NewTransport("default", http.DefaultTransport)

// Client is an http Client that uses the default GCE transport.
var Client = &http.Client{Transport: Transport}

// NewTransport returns a transport that uses the provided GCE
// serviceAccount (optional) to add authentication headers and then
// uses the provided underlying "base" transport.
//
// For more information on Service Accounts, see
// https://developers.google.com/compute/docs/authentication.
func NewTransport(serviceAccount string, base http.RoundTripper) http.RoundTripper {
	if serviceAccount == "" {
		serviceAccount = "default"
	}
	return &transport{base: base, acct: serviceAccount}
}

type transport struct {
	base http.RoundTripper
	acct string

	mu      sync.Mutex
	token   string
	expires time.Time
}

func (t *transport) getToken() (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.token != "" && t.expires.After(time.Now().Add(2*time.Second)) {
		return t.token, nil
	}
	tokenJSON, err := MetadataValue("instance/service-accounts/" + t.acct + "/token")
	if err != nil {
		return "", err
	}
	var token struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(strings.NewReader(tokenJSON)).Decode(&token); err != nil {
		return "", err
	}
	if token.AccessToken == "" {
		return "", errors.New("no access token returned")
	}
	t.token = token.AccessToken
	t.expires = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
	return t.token, nil
}

// cloneRequest returns a clone of the provided *http.Request.
// The clone is a shallow copy of the struct and its Header map.
func cloneRequest(r *http.Request) *http.Request {
	// shallow copy of the struct
	r2 := new(http.Request)
	*r2 = *r
	// deep copy of the Header
	r2.Header = make(http.Header)
	for k, s := range r.Header {
		r2.Header[k] = s
	}
	return r2
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	token, err := t.getToken()
	if err != nil {
		return nil, err
	}

	newReq := cloneRequest(req)
	newReq.Header.Set("Authorization", "Bearer "+token)

	// Needed for some APIs, like Google Cloud Storage?
	// See https://developers.google.com/storage/docs/projects
	// Which despite saying XML, also seems to fix JSON API?
	projID, _ := ProjectID()
	newReq.Header["x-goog-project-id"] = []string{projID}

	return t.base.RoundTrip(newReq)
}
