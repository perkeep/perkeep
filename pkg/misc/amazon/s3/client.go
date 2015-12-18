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
	"encoding/hex"
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
	"strings"
	"time"

	"camlistore.org/pkg/blob"
)

const maxList = 1000

// Client is an Amazon S3 client.
type Client struct {
	*Auth
	Transport http.RoundTripper // or nil for the default
}

type Bucket struct {
	Name         string
	CreationDate string // 2006-02-03T16:45:09.000Z
}

func (c *Client) transport() http.RoundTripper {
	if c.Transport != nil {
		return c.Transport
	}
	return http.DefaultTransport
}

// bucketURL returns the URL prefix of the bucket, with trailing slash
func (c *Client) bucketURL(bucket string) string {
	if IsValidBucket(bucket) && !strings.Contains(bucket, ".") {
		return fmt.Sprintf("https://%s.%s/", bucket, c.hostname())
	}
	return fmt.Sprintf("https://%s/%s/", c.hostname(), bucket)
}

func (c *Client) keyURL(bucket, key string) string {
	return c.bucketURL(bucket) + key
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
	res, err := c.transport().RoundTrip(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
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
func (c *Client) Stat(key, bucket string) (size int64, reterr error) {
	req := newReq(c.keyURL(bucket, key))
	req.Method = "HEAD"
	c.Auth.SignRequest(req)
	res, err := c.transport().RoundTrip(req)
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
	return 0, fmt.Errorf("s3: Unexpected status code %d statting object %v", res.StatusCode, key)
}

func (c *Client) PutObject(key, bucket string, md5 hash.Hash, size int64, body io.Reader) error {
	req := newReq(c.keyURL(bucket, key))
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

	res, err := c.transport().RoundTrip(req)
	if res != nil && res.Body != nil {
		defer res.Body.Close()
	}
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		// res.Write(os.Stderr)
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

// BucketLocation returns the S3 hostname to be used with the given bucket.
func (c *Client) BucketLocation(bucket string) (location string, err error) {
	if !strings.HasSuffix(c.hostname(), "amazonaws.com") {
		return "", errors.New("BucketLocation not implemented for non-Amazon S3 hostnames")
	}
	url_ := fmt.Sprintf("https://s3.amazonaws.com/%s/?location", url.QueryEscape(bucket))
	req := newReq(url_)
	c.Auth.SignRequest(req)
	res, err := c.transport().RoundTrip(req)
	if err != nil {
		return
	}
	var xres xmlLocationConstraint
	if err := xml.NewDecoder(res.Body).Decode(&xres); err != nil {
		return "", err
	}
	if xres.Location == "" {
		return "s3.amazonaws.com", nil
	}
	return "s3-" + xres.Location + ".amazonaws.com", nil
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

		url_ := fmt.Sprintf("%s?marker=%s&max-keys=%d",
			c.bucketURL(bucket), url.QueryEscape(marker), fetchN)

		// Try the enumerate three times, since Amazon likes to close
		// https connections a lot, and Go sucks at dealing with it:
		// https://code.google.com/p/go/issues/detail?id=3514
		const maxTries = 5
		for try := 1; try <= maxTries; try++ {
			time.Sleep(time.Duration(try-1) * 100 * time.Millisecond)
			req := newReq(url_)
			c.Auth.SignRequest(req)
			res, err := c.transport().RoundTrip(req)
			if err != nil {
				if try < maxTries {
					continue
				}
				return nil, err
			}
			if res.StatusCode != http.StatusOK {
				if res.StatusCode < 500 {
					body, _ := ioutil.ReadAll(io.LimitReader(res.Body, 1<<20))
					aerr := &Error{
						Op:     "ListBucket",
						Code:   res.StatusCode,
						Body:   body,
						Header: res.Header,
					}
					aerr.parseXML()
					res.Body.Close()
					return nil, aerr
				}
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
			res.Body.Close()
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
	req := newReq(c.keyURL(bucket, key))
	c.Auth.SignRequest(req)
	res, err := c.transport().RoundTrip(req)
	if err != nil {
		return
	}
	switch res.StatusCode {
	case http.StatusOK:
		return res.Body, res.ContentLength, nil
	case http.StatusNotFound:
		res.Body.Close()
		return nil, 0, os.ErrNotExist
	default:
		res.Body.Close()
		return nil, 0, fmt.Errorf("Amazon HTTP error on GET: %d", res.StatusCode)
	}
}

// GetPartial fetches part of the s3 key object in bucket.
// If length is negative, the rest of the object is returned.
// The caller must close rc.
func (c *Client) GetPartial(bucket, key string, offset, length int64) (rc io.ReadCloser, err error) {
	if offset < 0 {
		return nil, errors.New("invalid negative offset")
	}

	req := newReq(c.keyURL(bucket, key))
	if length >= 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, offset+length-1))
	} else {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	c.Auth.SignRequest(req)

	res, err := c.transport().RoundTrip(req)
	if err != nil {
		return
	}
	switch res.StatusCode {
	case http.StatusOK, http.StatusPartialContent:
		return res.Body, nil
	case http.StatusNotFound:
		res.Body.Close()
		return nil, os.ErrNotExist
	case http.StatusRequestedRangeNotSatisfiable:
		res.Body.Close()
		return nil, blob.ErrOutOfRangeOffsetSubFetch
	default:
		res.Body.Close()
		return nil, fmt.Errorf("Amazon HTTP error on GET: %d", res.StatusCode)
	}
}

func (c *Client) Delete(bucket, key string) error {
	req := newReq(c.keyURL(bucket, key))
	req.Method = "DELETE"
	c.Auth.SignRequest(req)
	res, err := c.transport().RoundTrip(req)
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

// IsValid reports whether bucket is a valid bucket name, per Amazon's naming restrictions.
//
// See http://docs.aws.amazon.com/AmazonS3/latest/dev/BucketRestrictions.html
func IsValidBucket(bucket string) bool {
	l := len(bucket)
	if l < 3 || l > 63 {
		return false
	}

	valid := false
	prev := byte('.')
	for i := 0; i < len(bucket); i++ {
		c := bucket[i]
		switch {
		default:
			return false
		case 'a' <= c && c <= 'z':
			valid = true
		case '0' <= c && c <= '9':
			// Is allowed, but bucketname can't be just numbers.
			// Therefore, don't set valid to true
		case c == '-':
			if prev == '.' {
				return false
			}
		case c == '.':
			if prev == '.' || prev == '-' {
				return false
			}
		}
		prev = c
	}

	if prev == '-' || prev == '.' {
		return false
	}
	return valid
}

// Error is the type returned by some API operations.
//
// TODO: it should be more/all of them.
type Error struct {
	Op     string
	Code   int         // HTTP status code
	Body   []byte      // response body
	Header http.Header // response headers

	// UsedEndpoint and AmazonCode are the XML response's Endpoint and
	// Code fields, respectively.
	UseEndpoint string // if a temporary redirect (wrong hostname)
	AmazonCode  string
}

func (e *Error) Error() string {
	if bytes.Contains(e.Body, []byte("<Error>")) {
		return fmt.Sprintf("s3.%s: status %d: %s", e.Op, e.Code, e.Body)
	}
	return fmt.Sprintf("s3.%s: status %d", e.Op, e.Code)
}

func (e *Error) parseXML() {
	var xe xmlError
	_ = xml.NewDecoder(bytes.NewReader(e.Body)).Decode(&xe)
	e.AmazonCode = xe.Code
	if xe.Code == "TemporaryRedirect" {
		e.UseEndpoint = xe.Endpoint
	}
	if xe.Code == "SignatureDoesNotMatch" {
		want, _ := hex.DecodeString(strings.Replace(xe.StringToSignBytes, " ", "", -1))
		log.Printf("S3 SignatureDoesNotMatch. StringToSign should be %d bytes: %q (%x)", len(want), want, want)
	}

}

// xmlError is the Error response from Amazon.
type xmlError struct {
	XMLName           xml.Name `xml:"Error"`
	Code              string
	Message           string
	RequestId         string
	Bucket            string
	Endpoint          string
	StringToSignBytes string
}

// xmlLocationConstraint is the LocationConstraint returned from BucketLocation.
type xmlLocationConstraint struct {
	XMLName  xml.Name `xml:"LocationConstraint"`
	Location string   `xml:",chardata"`
}
