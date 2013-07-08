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

// Package googlestorage implements a generic Google Storage API
// client. It does not include any Camlistore-specific logic.
package googlestorage

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"camlistore.org/third_party/code.google.com/p/goauth2/oauth"
)

const (
	gsAccessURL = "https://storage.googleapis.com"
)

type Client struct {
	transport *oauth.Transport
	client    *http.Client
}

type Object struct {
	Bucket string
	Key    string
}

type SizedObject struct {
	Object
	Size int64
}

func NewClient(transport *oauth.Transport) *Client {
	return &Client{transport, transport.Client()}
}

func (gso Object) String() string {
	return fmt.Sprintf("%v/%v", gso.Bucket, gso.Key)
}

func (sgso SizedObject) String() string {
	return fmt.Sprintf("%v/%v (%vB)", sgso.Bucket, sgso.Key, sgso.Size)
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
	if resp, err = gsa.client.Do(req); err != nil {
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

// Fetch a GS object.
// Bucket and Key fields are trusted to be valid.
// Returns (object reader, object size, err).  Reader must be closed.
func (gsa *Client) GetObject(obj *Object) (io.ReadCloser, int64, error) {

	resp, err := gsa.simpleRequest("GET", gsAccessURL+"/"+obj.Bucket+"/"+obj.Key)
	if err != nil {
		return nil, 0, fmt.Errorf("GS GET request failed: %v\n", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("GS GET request failed status: %v\n", resp.Status)
	}

	return resp.Body, resp.ContentLength, nil
}

// Check for size / existence of a GS object.
// Bucket and Key fields are trusted to be valid.
// err signals io / authz errors, a nonexistant file is not an error.
func (gsa *Client) StatObject(obj *Object) (size int64, exists bool, err error) {
	resp, err := gsa.simpleRequest("HEAD", gsAccessURL+"/"+obj.Bucket+"/"+obj.Key)
	if err != nil {
		return
	}
	resp.Body.Close() // should be empty

	if resp.StatusCode == http.StatusNotFound {
		return
	}
	if resp.StatusCode == http.StatusOK {
		if size, err = strconv.ParseInt(resp.Header["Content-Length"][0], 10, 64); err != nil {
			return
		}
		return size, true, nil
	}

	// Any response other than 404 or 200 is erroneous
	return 0, false, fmt.Errorf("Bad head response code: %v", resp.Status)
}

// Upload a GS object.  Bucket and Key are trusted to be valid.
// shouldRetry will be true if the put failed due to authorization, but
// credentials have been refreshed and another attempt is likely to succeed.
// In this case, content will have been consumed.
func (gsa *Client) PutObject(obj *Object, content io.ReadCloser) (shouldRetry bool, err error) {
	objURL := gsAccessURL + "/" + obj.Bucket + "/" + obj.Key
	var req *http.Request
	if req, err = http.NewRequest("PUT", objURL, content); err != nil {
		return
	}
	req.Header.Set("x-goog-api-version", "2")

	var resp *http.Response
	if resp, err, shouldRetry = gsa.doRequest(req, false); err != nil {
		return
	}
	resp.Body.Close() // should be empty

	if resp.StatusCode != http.StatusOK {
		return shouldRetry, fmt.Errorf("Bad put response code: %v", resp.Status)
	}
	return
}

// Removes a GS object.
// Bucket and Key values are trusted to be valid.
func (gsa *Client) DeleteObject(obj *Object) (err error) {
	//	bucketURL := gsAccessURL + "/" + obj.Bucket + "/" + obj.Key
	resp, err := gsa.simpleRequest("DELETE", gsAccessURL+"/"+obj.Bucket+"/"+obj.Key)
	if err != nil {
		return
	}
	if resp.StatusCode != http.StatusNoContent {
		err = fmt.Errorf("Bad delete response code: %v", resp.Status)
	}
	return
}

// Used for unmarshalling XML returned by enumerate request
type gsListResult struct {
	Contents []SizedObject
}

// List the objects in a GS bucket.
// If after is nonempty, listing will begin with lexically greater object names
// If limit is nonzero, the length of the list will be limited to that number.
func (gsa *Client) EnumerateObjects(bucket, after string, limit int) ([]SizedObject, error) {
	// Build url, with query params
	params := make([]string, 0, 2)
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

	// Make the request
	resp, err := gsa.simpleRequest("GET", gsAccessURL+"/"+bucket+"/"+query)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Bad enumerate response code: %v", resp.Status)
	}

	// Parse the XML response
	result := &gsListResult{make([]SizedObject, 0, limit)}
	if err = xml.NewDecoder(resp.Body).Decode(result); err != nil {
		return nil, err
	}
	// Fill in the Bucket on all the SizedObjects
	for i, _ := range result.Contents {
		result.Contents[i].Bucket = bucket
	}

	return result.Contents, nil
}
