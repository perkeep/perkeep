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
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"camlistore.org/pkg/auth"
)

type Client struct {
	// server is the input from user, pre-discovery.
	// For example "http://foo.com" or "foo.com:1234".
	// It is the responsibility of initPrefix to parse
	// server and set prefix, including doing discovery
	// to figure out what the proper server-declared
	// prefix is.
	server string

	prefixOnce sync.Once
	prefixv    string // URL prefix before "/camli/"
	prefixErr  error

	authMode auth.AuthMode

	httpClient *http.Client

	statsMutex sync.Mutex
	stats      Stats

	log *log.Logger // not nil
}

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
	// TODO(bradfitz): implement.  do discovery, like in initPrefix(), merging the prefix discovery.
	return "", errors.New("TODO: implement searchRoot discovery")
}

func (c *Client) prefix() (string, error) {
	c.prefixOnce.Do(func() { c.initPrefix() })
	if c.prefixErr != nil {
		return "", c.prefixErr
	}
	return c.prefixv, nil
}

func (c *Client) initPrefix() {
	s := c.server
	if !strings.HasPrefix(s, "http") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		c.prefixErr = err
		return
	}
	if len(u.Path) > 1 {
		c.prefixv = strings.TrimRight(s, "/")
		return
	}
	// If the path is just "" or "/", do discovery against
	// the URL to see which path we should actually use.
	req, _ := http.NewRequest("GET", u.String(), nil)
	req.Header.Set("Accept", "text/x-camli-configuration")
	res, err := c.httpClient.Do(req)
	if err != nil {
		c.prefixErr = err
		return
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		c.prefixErr = fmt.Errorf("Got status %q from blobserver during configuration discovery", res.Status)
		return
	}
	// TODO(bradfitz): little weird in retrospect that we request
	// text/x-camli-configuration and expect to get back
	// text/javascript.  Make them consistent.
	if ct := res.Header.Get("Content-Type"); ct != "text/javascript" {
		c.prefixErr = fmt.Errorf("Blobserver returned unexpected type %q from discovery", ct)
		return
	}
	m := make(map[string]interface{})
	if err := json.NewDecoder(res.Body).Decode(&m); err != nil {
		c.prefixErr = err
		return
	}
	blobRoot, ok := m["blobRoot"].(string)
	if !ok {
		c.prefixErr = fmt.Errorf("No blobRoot in config discovery response")
		return
	}
	u.Path = blobRoot
	c.prefixv = strings.TrimRight(u.String(), "/")
	log.Printf("set prefix to %q", c.prefixv)
}

func (c *Client) newRequest(method, url string) *http.Request {
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		panic(err.Error())
	}
	c.authMode.AddAuthHeader(req)
	return req
}
