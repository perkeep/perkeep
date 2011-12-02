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
	"fmt"
	"http"
	"log"
	"os"
	"sync"

	"camli/auth"
)

type Client struct {
	server   string // URL prefix before "/camli/"
	authMode auth.AuthMode

	httpClient *http.Client

	statsMutex sync.Mutex
	stats      Stats

	log *log.Logger // not nil
}

type Stats struct {
	// The number of uploads that were requested, but perhaps
	// not actually performed if the server already had the items.
	UploadRequests ByCountAndBytes

	// The uploads which were actually sent to the blobserver
	// due to the server not having the blobs
	Uploads ByCountAndBytes
}

func (s *Stats) String() string {
	return "[uploadRequests=" + s.UploadRequests.String() + " uploads=" + s.Uploads.String() + "]"
}

type ByCountAndBytes struct {
	Blobs int
	Bytes int64
}

func (bb *ByCountAndBytes) String() string {
	return fmt.Sprintf("[blobs=%d bytes=%d]", bb.Blobs, bb.Bytes)
}

func New(server string) *Client {
	return &Client{
		server:     server,
		httpClient: http.DefaultClient,
	}
}

func (c *Client) SetHttpClient(client *http.Client) {
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

type devNullWriter struct{}

func (_ *devNullWriter) Write(p []byte) (int, os.Error) {
	return len(p), nil
}

func (c *Client) SetLogger(logger *log.Logger) {
	if logger == nil {
		c.log = log.New(&devNullWriter{}, "", 0)
	} else {
		c.log = logger
	}
}

func (c *Client) Stats() Stats {
	c.statsMutex.Lock()
	defer c.statsMutex.Unlock()
	return c.stats // copy
}

func (c *Client) newRequest(method, url string) *http.Request {
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		panic(err.String())
	}
	c.authMode.AddAuthHeader(req)
	return req
}
