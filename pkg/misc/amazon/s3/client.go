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

// Package s3 implements a generic Amazon S3 client, not specific
// to Camlistore.
package s3

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strconv"
)

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

func newReq(url_ string) *http.Request {
	req, err := http.NewRequest("GET", url_, nil)
	if err != nil {
		panic(fmt.Sprintf("s3 client; invalid URL: %v", err))
	}
	req.Header.Set("User-Agent", "go-camlistore-s3")
	return req
}

func (c *Client) Buckets() ([]*Bucket, error) {
	req := newReq("https://s3.amazonaws.com/")
	c.Auth.SignRequest(req)
	res, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("s3: Unexpected status code %d fetching bucket list", res.StatusCode)
	}
	return parseListAllMyBuckets(res.Body)
}

func parseListAllMyBuckets(r io.Reader) ([]*Bucket, error) {
	type allMyBuckets struct {
		Buckets struct {
			Bucket []*Bucket
		}
	}
	var res allMyBuckets
	if err := xml.NewDecoder(r).Decode(&res); err != nil {
		return nil, err
	}
	return res.Buckets.Bucket, nil
}

// Returns 0, os.ErrNotExist if not on S3, otherwise reterr is real.
func (c *Client) Stat(name, bucket string) (size int64, reterr error) {
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
		return 0, os.ErrNotExist
	}
	return strconv.ParseInt(res.Header.Get("Content-Length"), 10, 64)
}

func (c *Client) PutObject(name, bucket string, md5 hash.Hash, size int64, body io.Reader) error {
	req := newReq("http://" + bucket + ".s3.amazonaws.com/" + name)
	req.Method = "PUT"
	req.ContentLength = size
	if md5 != nil {
		b64 := new(bytes.Buffer)
		encoder := base64.NewEncoder(base64.StdEncoding, b64)
		encoder.Write(md5.Sum(nil))
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
	Contents    []*Item
	IsTruncated bool
}

// marker returns the string lexically greater than the provided s,
// if s is not empty.
func marker(s string) string {
	if s == "" {
		return s
	}
	b := []byte(s)
	i := len(b)
	for i > 0 {
		i--
		b[i]++
		if b[i] != 0 {
			break
		}
	}
	return string(b)
}

// ListBucket returns 0 to maxKeys (inclusive) items from the provided
// bucket.  The items will have keys greater than the provided after, which
// may be empty.  (Note: this is not greater than or equal to, like the S3
// API's 'marker' parameter).  If the length of the returned items is equal
// to maxKeys, there is no indication whether or not the returned list is
// truncated.
func (c *Client) ListBucket(bucket string, after string, maxKeys int) (items []*Item, err error) {
	if maxKeys < 0 {
		return nil, errors.New("invalid negative maxKeys")
	}
	const s3APIMaxFetch = 1000
	for len(items) < maxKeys {
		fetchN := maxKeys - len(items)
		if fetchN > s3APIMaxFetch {
			fetchN = s3APIMaxFetch
		}
		var bres listBucketResults
		url_ := fmt.Sprintf("http://%s.s3.amazonaws.com/?marker=%s&max-keys=%d",
			bucket, url.QueryEscape(marker(after)), fetchN)
		req := newReq(url_)
		c.Auth.SignRequest(req)
		res, err := c.httpClient().Do(req)
		if err != nil {
			return nil, err
		}
		if err := xml.NewDecoder(res.Body).Decode(&bres); err != nil {
			return nil, err
		}
		res.Body.Close()
		for _, it := range bres.Contents {
			if it.Key <= after {
				return nil, fmt.Errorf("Unexpected response from Amazon: item key %q but wanted greater than %q", it.Key, after)
			}
			items = append(items, it)
			after = it.Key
		}
		if !bres.IsTruncated {
			break
		}
	}
	return items, nil
}

func (c *Client) Get(bucket, key string) (body io.ReadCloser, size int64, err error) {
	url_ := fmt.Sprintf("http://%s.s3.amazonaws.com/%s", bucket, key)
	req := newReq(url_)
	c.Auth.SignRequest(req)
	var res *http.Response
	res, err = c.httpClient().Do(req)
	if err != nil {
		return
	}
	if res.StatusCode != http.StatusOK && res != nil && res.Body != nil {
		defer func() {
			io.Copy(os.Stderr, res.Body)
		}()
	}
	if res.StatusCode == http.StatusNotFound {
		err = os.ErrNotExist
		return
	}
	if res.StatusCode != http.StatusOK {
		err = fmt.Errorf("Amazon HTTP error on GET: %d", res.StatusCode)
		return
	}
	return res.Body, res.ContentLength, nil
}

func (c *Client) Delete(bucket, key string) error {
	url_ := fmt.Sprintf("http://%s.s3.amazonaws.com/%s", bucket, key)
	req := newReq(url_)
	req.Method = "DELETE"
	c.Auth.SignRequest(req)
	res, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	if res != nil && res.Body != nil {
		defer res.Body.Close()
	}
	if res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusNoContent ||
		res.StatusCode == http.StatusOK {
		return nil
	}
	return fmt.Errorf("Amazon HTTP error on DELETE: %d", res.StatusCode)
}
