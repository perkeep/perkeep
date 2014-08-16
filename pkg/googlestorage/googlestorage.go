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

// Package googlestorage is simple Google Cloud Storage client.
//
// It does not include any Camlistore-specific logic.
package googlestorage

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"camlistore.org/pkg/httputil"
	"camlistore.org/third_party/code.google.com/p/goauth2/oauth"
	api "camlistore.org/third_party/code.google.com/p/google-api-go-client/storage/v1"
	"camlistore.org/third_party/github.com/bradfitz/gce"
)

const (
	gsAccessURL = "https://storage.googleapis.com"
)

// A Client provides access to Google Cloud Storage.
type Client struct {
	client    *http.Client
	transport *oauth.Transport // nil for service clients
	service   *api.Service
}

// An Object holds the name of an object (its bucket and key) within
// Google Cloud Storage.
type Object struct {
	Bucket string
	Key    string
}

func (o *Object) valid() error {
	if o == nil {
		return errors.New("invalid nil Object")
	}
	if o.Bucket == "" {
		return errors.New("missing required Bucket field in Object")
	}
	if o.Key == "" {
		return errors.New("missing required Key field in Object")
	}
	return nil
}

// A SizedObject holds the bucket, key, and size of an object.
type SizedObject struct {
	Object
	Size int64
}

// NewServiceClient returns a Client for use when running on Google
// Compute Engine.  This client can access buckets owned by the samre
// project ID as the VM.
func NewServiceClient() (*Client, error) {
	if !gce.OnGCE() {
		return nil, errors.New("not running on Google Compute Engine")
	}
	scopes, _ := gce.Scopes("default")
	if !scopes.Contains("https://www.googleapis.com/auth/devstorage.full_control") &&
		!scopes.Contains("https://www.googleapis.com/auth/devstorage.read_write") {
		return nil, errors.New("when this Google Compute Engine VM instance was created, it wasn't granted access to Cloud Storage")
	}
	service, _ := api.New(gce.Client)
	return &Client{client: gce.Client, service: service}, nil
}

func NewClient(transport *oauth.Transport) *Client {
	client := transport.Client()
	service, _ := api.New(client)
	return &Client{
		client:    transport.Client(),
		transport: transport,
		service:   service,
	}
}

func (o *Object) String() string {
	if o == nil {
		return "<nil *Object>"
	}
	return fmt.Sprintf("%v/%v", o.Bucket, o.Key)
}

func (so SizedObject) String() string {
	return fmt.Sprintf("%v/%v (%vB)", so.Bucket, so.Key, so.Size)
}

// A close relative to http.Client.Do(), helping with token refresh logic.
// If canResend is true and the initial request's response is an auth error
// (401 or 403), oauth credentials will be refreshed and the request sent
// again.  This should only be done for requests with empty bodies, since the
// Body will be consumed on the first attempt if it exists.
// If canResend is false, and req would have been resent if canResend were
// true, then shouldRetry will be true.
// One of resp or err will always be nil.
func (gsa *Client) doRequest(req *http.Request, canResend bool) (resp *http.Response, err error, shouldRetry bool) {
	resp, err = gsa.client.Do(req)
	if err != nil {
		return
	}
	if gsa.transport == nil {
		return
	}

	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		// Unauth.  Perhaps tokens need refreshing?
		if err = gsa.transport.Refresh(); err != nil {
			return
		}
		// Refresh succeeded.  req should be resent
		if !canResend {
			return resp, nil, true
		}
		// Resend req.  First, need to close the soon-overwritten response Body
		resp.Body.Close()
		resp, err = gsa.client.Do(req)
	}

	return
}

// Makes a simple body-less google storage request
func (gsa *Client) simpleRequest(method, url_ string) (resp *http.Response, err error) {
	// Construct the request
	req, err := http.NewRequest(method, url_, nil)
	if err != nil {
		return
	}
	req.Header.Set("x-goog-api-version", "2")

	resp, err, _ = gsa.doRequest(req, true)
	return
}

// GetObject fetches a Google Cloud Storage object.
// The caller must close rc.
func (gsa *Client) GetObject(obj *Object) (rc io.ReadCloser, size int64, err error) {
	if err = obj.valid(); err != nil {
		return
	}
	resp, err := gsa.simpleRequest("GET", gsAccessURL+"/"+obj.Bucket+"/"+obj.Key)
	if err != nil {
		return nil, 0, fmt.Errorf("GS GET request failed: %v\n", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, 0, os.ErrNotExist
	}
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("GS GET request failed status: %v\n", resp.Status)
	}

	return resp.Body, resp.ContentLength, nil
}

// StatObject checks for the size & existence of a Google Cloud Storage object.
// Non-existence of a file is not an error.
func (gsa *Client) StatObject(obj *Object) (size int64, exists bool, err error) {
	if err = obj.valid(); err != nil {
		return
	}
	res, err := gsa.simpleRequest("HEAD", gsAccessURL+"/"+obj.Bucket+"/"+obj.Key)
	if err != nil {
		return
	}
	res.Body.Close() // per contract but unnecessary for most RoundTrippers

	switch res.StatusCode {
	case http.StatusNotFound:
		return 0, false, nil
	case http.StatusOK:
		if size, err = strconv.ParseInt(res.Header["Content-Length"][0], 10, 64); err != nil {
			return
		}
		return size, true, nil
	default:
		return 0, false, fmt.Errorf("Bad head response code: %v", res.Status)
	}
}

// PutObject uploads a Google Cloud Storage object.
// shouldRetry will be true if the put failed due to authorization, but
// credentials have been refreshed and another attempt is likely to succeed.
// In this case, content will have been consumed.
func (gsa *Client) PutObject(obj *Object, content io.Reader) (shouldRetry bool, err error) {
	if err := obj.valid(); err != nil {
		return false, err
	}
	const maxSlurp = 2 << 20
	var buf bytes.Buffer
	n, err := io.CopyN(&buf, content, maxSlurp)
	if err != nil && err != io.EOF {
		return false, err
	}
	contentType := http.DetectContentType(buf.Bytes())
	if contentType == "application/octet-stream" && n < maxSlurp && utf8.Valid(buf.Bytes()) {
		contentType = "text/plain; charset=utf-8"
	}

	objURL := gsAccessURL + "/" + obj.Bucket + "/" + obj.Key
	var req *http.Request
	if req, err = http.NewRequest("PUT", objURL, ioutil.NopCloser(io.MultiReader(&buf, content))); err != nil {
		return
	}
	req.Header.Set("x-goog-api-version", "2")
	req.Header.Set("Content-Type", contentType)

	var resp *http.Response
	if resp, err, shouldRetry = gsa.doRequest(req, false); err != nil {
		return
	}

	if resp.StatusCode != http.StatusOK {
		return shouldRetry, fmt.Errorf("Bad put response code: %v", resp.Status)
	}
	return
}

// DeleteObject removes an object.
func (gsa *Client) DeleteObject(obj *Object) error {
	if err := obj.valid(); err != nil {
		return err
	}
	resp, err := gsa.simpleRequest("DELETE", gsAccessURL+"/"+obj.Bucket+"/"+obj.Key)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("Error deleting %v: bad delete response code: %v", obj, resp.Status)
	}
	return nil
}

// EnumerateObjects lists the objects in a bucket.
// If after is non-empty, listing will begin with lexically greater object names.
// If limit is non-zero, the length of the list will be limited to that number.
func (gsa *Client) EnumerateObjects(bucket, after string, limit int) ([]SizedObject, error) {
	// Build url, with query params
	var params []string
	if after != "" {
		params = append(params, "marker="+url.QueryEscape(after))
	}
	if limit > 0 {
		params = append(params, fmt.Sprintf("max-keys=%v", limit))
	}
	query := ""
	if len(params) > 0 {
		query = "?" + strings.Join(params, "&")
	}

	resp, err := gsa.simpleRequest("GET", gsAccessURL+"/"+bucket+"/"+query)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Bad enumerate response code: %v", resp.Status)
	}

	var xres struct {
		Contents []SizedObject
	}
	defer httputil.CloseBody(resp.Body)
	if err = xml.NewDecoder(resp.Body).Decode(&xres); err != nil {
		return nil, err
	}

	// Fill in the Bucket on all the SizedObjects
	for _, o := range xres.Contents {
		o.Bucket = bucket
	}

	return xres.Contents, nil
}

// BucketInfo returns information about a bucket.
func (c *Client) BucketInfo(bucket string) (*api.Bucket, error) {
	return c.service.Buckets.Get(bucket).Do()
}
