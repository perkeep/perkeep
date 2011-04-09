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
	"bytes"
	"encoding/base64"
	"fmt"
	"hash"
	"http"
	"io"
	"io/ioutil"
	"log"
	"os"
	"strconv"
	"xml"
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
		Host:       u.Host,
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
	res.Body.Close()
	log.Printf("got: %q", string(slurp))
	return nil, nil
}

// Returns 0, os.ENOENT if not on S3, otherwise reterr is real.
func (c *Client) Stat(name, bucket string) (size int64, reterr os.Error) {
	defer func() {
		log.Printf("s3 client: Stat(%q, %q) = %d, %v", name, bucket, size, reterr)
	}()
	req := newReq("http://" + bucket + ".s3.amazonaws.com/" + name)
	req.Method = "HEAD"
	c.Auth.SignRequest(req)
	res, err := c.httpClient().Do(req)
	if err != nil {
		return 0, err
	}
	if res.Body != nil {
		defer res.Body.Close()
	}
	if res.StatusCode == http.StatusNotFound {
		return 0, os.ENOENT
	}
	return strconv.Atoi64(res.Header.Get("Content-Length"))
}

func (c *Client) PutObject(name, bucket string, md5 hash.Hash, size int64, body io.Reader) os.Error {
	req := newReq("http://" + bucket + ".s3.amazonaws.com/" + name)
	req.Method = "PUT"
	req.ContentLength = size
	if md5 != nil {
		b64 := new(bytes.Buffer)
		encoder := base64.NewEncoder(base64.StdEncoding, b64)
		encoder.Write(md5.Sum())
		encoder.Close()
		req.Header.Set("Content-MD5", b64.String())
	}
	c.Auth.SignRequest(req)
	req.Body = ioutil.NopCloser(body)

	res, err := c.httpClient().Do(req)
	if res != nil && res.Body != nil {
		defer res.Body.Close()
	}
	if err != nil {
		return err
	}
	if res.StatusCode != 200 {
		res.Write(os.Stderr)
		return fmt.Errorf("Got response code %d from s3", res.StatusCode)
	}
	return nil
}

type Item struct {
	Key  string
	Size int64
}

type listBucketResults struct {
	Contents []*Item
}

func (c *Client) ListBucket(bucket string, after string, maxKeys uint) (items []*Item, reterr os.Error) {
	var bres listBucketResults
	url := fmt.Sprintf("http://%s.s3.amazonaws.com/?marker=%s&max-keys=%d",
		bucket, http.URLEscape(after), maxKeys)
	req := newReq(url)
	c.Auth.SignRequest(req)
	res, err := c.httpClient().Do(req)
	if res != nil && res.Body != nil {
		defer res.Body.Close()
	}
	if err != nil {
		return nil, err
	}
	if err := xml.Unmarshal(res.Body, &bres); err != nil {
		return nil, err
	}
	return bres.Contents, nil
}
