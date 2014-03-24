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
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"camlistore.org/pkg/httputil"
)

const maxList = 1000

// Client is an Amazon S3 client.
type Client struct {
	*Auth
	HTTPClient *http.Client // or nil for default client
}

type Bucket struct {
	Name         string
	CreationDate string // 2006-02-03T16:45:09.000Z
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
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
	req := newReq("https://" + c.hostname() + "/")
	c.Auth.SignRequest(req)
	res, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer httputil.CloseBody(res.Body)
	if res.StatusCode != http.StatusOK {
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
	req := newReq("https://" + bucket + "." + c.hostname() + "/" + name)
	req.Method = "HEAD"
	c.Auth.SignRequest(req)
	res, err := c.httpClient().Do(req)
	if err != nil {
		return 0, err
	}
	if res.Body != nil {
		defer res.Body.Close()
	}
	switch res.StatusCode {
	case http.StatusNotFound:
		return 0, os.ErrNotExist
	case http.StatusOK:
		return strconv.ParseInt(res.Header.Get("Content-Length"), 10, 64)
	}
	return 0, fmt.Errorf("s3: Unexpected status code %d statting object %v", res.StatusCode, name)
}

func (c *Client) PutObject(name, bucket string, md5 hash.Hash, size int64, body io.Reader) error {
	req := newReq("https://" + bucket + "." + c.hostname() + "/" + name)
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
		defer httputil.CloseBody(res.Body)
	}
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
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
	MaxKeys     int
	Name        string // bucket name
	Marker      string
}

// ListBucket returns 0 to maxKeys (inclusive) items from the provided
// bucket. Keys before startAt will be skipped. (This is the S3
// 'marker' value). If the length of the returned items is equal to
// maxKeys, there is no indication whether or not the returned list is
// truncated.
func (c *Client) ListBucket(bucket string, startAt string, maxKeys int) (items []*Item, err error) {
	if maxKeys < 0 {
		return nil, errors.New("invalid negative maxKeys")
	}
	marker := startAt
	for len(items) < maxKeys {
		fetchN := maxKeys - len(items)
		if fetchN > maxList {
			fetchN = maxList
		}
		var bres listBucketResults

		url_ := fmt.Sprintf("https://%s.%s/?marker=%s&max-keys=%d",
			bucket, c.hostname(), url.QueryEscape(marker), fetchN)

		// Try the enumerate three times, since Amazon likes to close
		// https connections a lot, and Go sucks at dealing with it:
		// https://code.google.com/p/go/issues/detail?id=3514
		const maxTries = 5
		for try := 1; try <= maxTries; try++ {
			time.Sleep(time.Duration(try-1) * 100 * time.Millisecond)
			req := newReq(url_)
			c.Auth.SignRequest(req)
			res, err := c.httpClient().Do(req)
			if err != nil {
				if try < maxTries {
					continue
				}
				return nil, err
			}
			if res.StatusCode != http.StatusOK {
				err = fmt.Errorf("s3.enumerate: status code %v", res.StatusCode)
			} else {
				bres = listBucketResults{}
				var logbuf bytes.Buffer
				err = xml.NewDecoder(io.TeeReader(res.Body, &logbuf)).Decode(&bres)
				if err != nil {
					log.Printf("Error parsing s3 XML response: %v for %q", err, logbuf.Bytes())
				} else if bres.MaxKeys != fetchN || bres.Name != bucket || bres.Marker != marker {
					err = fmt.Errorf("Unexpected parse from server: %#v from: %s", bres, logbuf.Bytes())
					log.Print(err)
				}
			}
			httputil.CloseBody(res.Body)
			if err != nil {
				if try < maxTries-1 {
					continue
				}
				log.Print(err)
				return nil, err
			}
			break
		}
		for _, it := range bres.Contents {
			if it.Key == marker && it.Key != startAt {
				// Skip first dup on pages 2 and higher.
				continue
			}
			if it.Key < startAt {
				return nil, fmt.Errorf("Unexpected response from Amazon: item key %q but wanted greater than %q", it.Key, startAt)
			}
			items = append(items, it)
			marker = it.Key
		}
		if !bres.IsTruncated {
			// log.Printf("Not truncated. so breaking. items = %d; len Contents = %d, url = %s", len(items), len(bres.Contents), url_)
			break
		}
	}
	return items, nil
}

func (c *Client) Get(bucket, key string) (body io.ReadCloser, size int64, err error) {
	url_ := fmt.Sprintf("https://%s.%s/%s", bucket, c.hostname(), key)
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
	url_ := fmt.Sprintf("https://%s.%s/%s", bucket, c.hostname(), key)
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
