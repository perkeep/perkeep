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

package s3

import (
	"fmt"
	"http"
	"io/ioutil"
	"log"
	"os"
)

var _ = log.Printf

type Client struct {
	*Auth
	HttpClient *http.Client // or nil for default client
}

type Bucket struct {
	Name         string
	CreationDate string // 2006-02-03T16:45:09.000Z
}

func (c *Client) httpClient() *http.Client {
	if c.HttpClient != nil {
		return c.HttpClient
	}
	return http.DefaultClient
}

func newReq(url string) *http.Request {
	u, err := http.ParseURL(url)
	if err != nil {
		panic(fmt.Sprintf("s3 client; invalid URL: %v", err))
	}
	return &http.Request{
		Method:     "GET",
		URL:        u,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     http.Header{},
		UserAgent:  "go-camlistore-s3",
	}
}

func (c *Client) Buckets() ([]*Bucket, os.Error) {
	req := newReq("https://s3.amazonaws.com/")
	c.Auth.SignRequest(req)
	res, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	slurp, _ := ioutil.ReadAll(res.Body)
	log.Printf("got: %q", string(slurp))
	return nil, nil
}
