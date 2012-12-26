/*
Copyright 2011 Google Inc.

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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"camlistore.org/pkg/auth"
	"camlistore.org/pkg/blobref"
)

// A Client provides access to a Camlistore server.
type Client struct {
	// server is the input from user, pre-discovery.
	// For example "http://foo.com" or "foo.com:1234".
	// It is the responsibility of initPrefix to parse
	// server and set prefix, including doing discovery
	// to figure out what the proper server-declared
	// prefix is.
	server string

	prefixOnce sync.Once
	prefixErr  error
	prefixv    string // URL prefix before "/camli/"

	discoOnce      sync.Once
	discoErr       error
	searchRoot     string // Handler prefix, or "" if none
	downloadHelper string // or "" if none

	authMode auth.AuthMode

	httpClient *http.Client

	statsMutex sync.Mutex
	stats      Stats

	log *log.Logger // not nil
}

// New returns a new Camlistore Client.
// The provided server is either "host:port" (assumed http, not https) or a
// URL prefix, with or without a path.
// Errors are not returned until subsequent operations.
func New(server string) *Client {
	return &Client{
		server:     server,
		httpClient: http.DefaultClient,
	}
}

// SetHTTPClient sets the Camlistore client's HTTP client.
// If nil, the default HTTP client is used.
func (c *Client) SetHTTPClient(client *http.Client) {
	if client == nil {
		client = http.DefaultClient
	}
	c.httpClient = client
}

func NewOrFail() *Client {
	log := log.New(os.Stderr, "", log.Ldate|log.Ltime)
	c := &Client{
		server:     blobServerOrDie(),
		httpClient: http.DefaultClient,
		log:        log,
	}
	err := c.SetupAuth()
	if err != nil {
		log.Fatal(err)
	}
	return c
}

func (c *Client) SetLogger(logger *log.Logger) {
	if logger == nil {
		c.log = log.New(ioutil.Discard, "", 0)
	} else {
		c.log = logger
	}
}

func (c *Client) Stats() Stats {
	c.statsMutex.Lock()
	defer c.statsMutex.Unlock()
	return c.stats // copy
}

// ErrNoSearchRoot is returned by SearchRoot if the server doesn't support search.
var ErrNoSearchRoot = errors.New("client: server doesn't support search")

func (c *Client) SearchRoot() (string, error) {
	c.condDiscovery()
	if c.discoErr != nil {
		return "", c.discoErr
	}
	if c.searchRoot == "" {
		return "", ErrNoSearchRoot
	}
	return c.searchRoot, nil
}

// SearchExistingFileSchema does a search query looking for an
// existing file with entire contents of wholeRef, then does a HEAD
// request to verify the file still exists on the server.  If so,
// it returns that file schema's blobref.
//
// May return (nil, nil) on ENOENT. A non-nil error is only returned
// if there were problems searching.
func (c *Client) SearchExistingFileSchema(wholeRef *blobref.BlobRef) (*blobref.BlobRef, error) {
	sr, err := c.SearchRoot()
	if err != nil {
		return nil, err
	}
	url := sr + "camli/search/files?wholedigest=" + wholeRef.String()
	req := c.newRequest("GET", url)
	res, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	body := io.TeeReader(io.LimitReader(res.Body, 1<<20), &buf)
	type justWriter struct {
		io.Writer
	}
	if res.StatusCode != 200 {
		io.Copy(justWriter{ioutil.Discard}, body) // golang.org/issue/4589
		return nil, fmt.Errorf("client: got status code %d from URL %s; body %s", res.StatusCode, url, buf.String())
	}
	var ress struct {
		Files []*blobref.BlobRef `json:"files"`
	}
	if err := json.NewDecoder(body).Decode(&ress); err != nil {
		io.Copy(justWriter{ioutil.Discard}, body) // golang.org/issue/4589
		return nil, fmt.Errorf("client: error parsing JSON from URL %s: %v; body=%s", url, err, buf.String())
	}
	if len(ress.Files) == 0 {
		return nil, nil
	}
	for _, f := range ress.Files {
		if c.FileHasContents(f, wholeRef) {
			return f, nil
		}
	}
	return nil, nil
}

// FileHasContents returns true iff f refers to a "file" or "bytes" schema blob,
// the server is configured with a "download helper", and the server responds
// that all chunks of 'f' are available and match the digest of wholeRef.
func (c *Client) FileHasContents(f, wholeRef *blobref.BlobRef) bool {
	c.condDiscovery()
	if c.discoErr != nil {
		return false
	}
	if c.downloadHelper == "" {
		return false
	}
	req := c.newRequest("HEAD", c.downloadHelper+f.String()+"/?verifycontents="+wholeRef.String())
	res, err := c.httpClient.Do(req)
	if err != nil {
		log.Printf("download helper HEAD error: %v", err)
		return false
	}
	defer res.Body.Close()
	return res.Header.Get("X-Camli-Contents") == wholeRef.String()
}

func (c *Client) prefix() (string, error) {
	c.prefixOnce.Do(func() { c.initPrefix() })
	if c.prefixErr != nil {
		return "", c.prefixErr
	}
	if c.discoErr != nil {
		return "", c.discoErr
	}
	return c.prefixv, nil
}

func (c *Client) discoRoot() string {
	s := c.server
	if !strings.HasPrefix(s, "http") {
		s = "http://" + s
	}
	return s
}

func (c *Client) initPrefix() {
	root := c.discoRoot()
	u, err := url.Parse(root)
	if err != nil {
		c.prefixErr = err
		return
	}
	if len(u.Path) > 1 {
		c.prefixv = strings.TrimRight(root, "/")
		return
	}
	c.condDiscovery()
}

func (c *Client) condDiscovery() {
	c.discoOnce.Do(func() { c.doDiscovery() })
}

func (c *Client) doDiscovery() {
	root, err := url.Parse(c.discoRoot())
	if err != nil {
		c.discoErr = err
		return
	}

	// If the path is just "" or "/", do discovery against
	// the URL to see which path we should actually use.
	req, _ := http.NewRequest("GET", c.discoRoot(), nil)
	req.Header.Set("Accept", "text/x-camli-configuration")
	c.authMode.AddAuthHeader(req)
	res, err := c.httpClient.Do(req)
	if err != nil {
		c.discoErr = err
		return
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		c.discoErr = fmt.Errorf("Got status %q from blobserver URL %q during configuration discovery", res.Status, c.discoRoot())
		return
	}
	// TODO(bradfitz): little weird in retrospect that we request
	// text/x-camli-configuration and expect to get back
	// text/javascript.  Make them consistent.
	if ct := res.Header.Get("Content-Type"); ct != "text/javascript" {
		c.discoErr = fmt.Errorf("Blobserver returned unexpected type %q from discovery", ct)
		return
	}
	m := make(map[string]interface{})
	if err := json.NewDecoder(res.Body).Decode(&m); err != nil {
		c.discoErr = err
		return
	}
	searchRoot, ok := m["searchRoot"].(string)
	if ok {
		u, err := root.Parse(searchRoot)
		if err != nil {
			c.discoErr = fmt.Errorf("client: invalid searchRoot %q; failed to resolve", searchRoot)
			return
		}
		c.searchRoot = u.String()
	}

	downloadHelper, ok := m["downloadHelper"].(string)
	if ok {
		u, err := root.Parse(downloadHelper)
		if err != nil {
			c.discoErr = fmt.Errorf("client: invalid downloadHelper %q; failed to resolve", downloadHelper)
			return
		}
		c.downloadHelper = u.String()
	}

	blobRoot, ok := m["blobRoot"].(string)
	if !ok {
		c.discoErr = fmt.Errorf("No blobRoot in config discovery response")
		return
	}
	u, err := root.Parse(blobRoot)
	if err != nil {
		c.discoErr = fmt.Errorf("client: error resolving blobRoot: %v", err)
		return
	}
	c.prefixv = strings.TrimRight(u.String(), "/")
}

func (c *Client) newRequest(method, url string) *http.Request {
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		panic(err.Error())
	}
	c.authMode.AddAuthHeader(req)
	return req
}
