/*
Copyright 2014 The Perkeep Authors

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

// Package storage implements a generic Azure storage client, not specific
// to Perkeep.
package storage

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"hash"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
)

const maxList = 5000

// Client is an Azure storage client.
type Client struct {
	*Auth
	Transport http.RoundTripper // or nil for the default

	// Hostname is the hostname to use in the requests.
	// By default its <account>.blob.core.windows.net.
	Hostname string
}

const defaultHost = "blob.core.windows.net"

func (c *Client) hostname() string {
	if c.Hostname != "" {
		return c.Hostname
	}
	if c.Account == "" {
		panic("Account not set")
	}
	return c.Account + "." + defaultHost
}

// Container is the result of an enumeration of containers
// TODO(gv): There are come more properties being exposed by Azure that we don't need right now
type Container struct {
	Name string
}

func (c *Client) transport() http.RoundTripper {
	if c.Transport != nil {
		return c.Transport
	}
	return http.DefaultTransport
}

// containerURL returns the URL prefix of the container, with trailing slash
func (c *Client) containerURL(container string) string {
	return fmt.Sprintf("https://%s/%s/", c.hostname(), container)
}

func (c *Client) keyURL(container, key string) string {
	return c.containerURL(container) + key
}

func newReq(ctx context.Context, url_ string) *http.Request {
	req, err := http.NewRequest("GET", url_, nil)
	if err != nil {
		panic(fmt.Sprintf("azure client; invalid URL: %v", err))
	}
	req.Header.Set("User-Agent", "go-camlistore-azure")
	req.Header.Set("x-ms-version", "2014-02-14")
	return req.WithContext(ctx)
}

// Containers list the containers active under the current account.
func (c *Client) Containers(ctx context.Context) ([]*Container, error) {
	req := newReq(ctx, "https://"+c.hostname()+"/")
	c.Auth.SignRequest(req)
	res, err := c.transport().RoundTrip(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("azure: Unexpected status code %d fetching container list", res.StatusCode)
	}
	return parseListAllMyContainers(res.Body)
}

func parseListAllMyContainers(r io.Reader) ([]*Container, error) {
	type allMyContainers struct {
		Containers struct {
			Container []*Container
		}
	}
	var res allMyContainers
	if err := xml.NewDecoder(r).Decode(&res); err != nil {
		return nil, err
	}
	return res.Containers.Container, nil
}

// Stat Stats a blob in Azure.
// It returns 0, os.ErrNotExist if not found on Azure, otherwise reterr is real.
func (c *Client) Stat(ctx context.Context, key, container string) (size int64, reterr error) {
	req := newReq(ctx, c.keyURL(container, key))
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
	return 0, fmt.Errorf("azure: Unexpected status code %d statting object %v", res.StatusCode, key)
}

// PutObject puts a blob to the specified container on Azure
func (c *Client) PutObject(ctx context.Context, key, container string, md5 hash.Hash, size int64, body io.Reader) error {
	req := newReq(ctx, c.keyURL(container, key))
	req.Method = "PUT"
	req.ContentLength = size
	if md5 != nil {
		b64 := new(bytes.Buffer)
		encoder := base64.NewEncoder(base64.StdEncoding, b64)
		encoder.Write(md5.Sum(nil))
		encoder.Close()
		req.Header.Set("Content-MD5", b64.String())
	}
	req.Header.Set("Content-Length", strconv.Itoa(int(size)))
	req.Header.Set("x-ms-blob-type", "BlockBlob")
	c.Auth.SignRequest(req)
	req.Body = io.NopCloser(body)

	res, err := c.transport().RoundTrip(req)
	if res != nil && res.Body != nil {
		defer res.Body.Close()
	}
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusCreated {
		if res.StatusCode < 500 {
			aerr := getAzureError("PutObject", res)
			return aerr
		}
		return fmt.Errorf("got response code %d from Azure", res.StatusCode)
	}
	return nil
}

// BlobProperties holds some information about the blobs.
// There are many more fields than just the one below, see:
// http://msdn.microsoft.com/en-us/library/azure/dd135734.aspx
type BlobProperties struct {
	ContentLength int `xml:"Content-Length"`
}

// Blob holds the name and properties of a blob when returned with a list-operation
type Blob struct {
	Name       string
	Properties BlobProperties
}

type listBlobsResults struct {
	Blobs struct {
		Blob []*Blob
	}
	MaxResults    int
	ContainerName string `xml:",attr"`
	Marker        string
	NextMarker    string
}

// ListBlobs returns 0 to maxKeys (inclusive) items from the provided
// container. If the length of the returned items is equal to maxKeys,
// there is no indication whether or not the returned list is truncated.
func (c *Client) ListBlobs(ctx context.Context, container string, maxResults int) (blobs []*Blob, err error) {
	if maxResults < 0 {
		return nil, errors.New("invalid negative maxKeys")
	}
	marker := ""
	for len(blobs) < maxResults {
		fetchN := min(maxResults-len(blobs), maxList)
		var bres listBlobsResults

		listURL := fmt.Sprintf("%s?restype=container&comp=list&marker=%s&maxresults=%d",
			c.containerURL(container), url.QueryEscape(marker), fetchN)

		req := newReq(ctx, listURL)
		c.Auth.SignRequest(req)

		res, err := c.transport().RoundTrip(req)
		if err != nil {
			return nil, err
		}
		if res.StatusCode != http.StatusOK {
			if res.StatusCode < 500 {
				aerr := getAzureError("ListBlobs", res)
				return nil, aerr
			}
		} else {
			bres = listBlobsResults{}
			var logbuf bytes.Buffer
			err = xml.NewDecoder(io.TeeReader(res.Body, &logbuf)).Decode(&bres)
			if err != nil {
				log.Printf("Error parsing Azure XML response: %v for %q", err, logbuf.Bytes())
			} else if bres.MaxResults != fetchN || bres.ContainerName != container || bres.Marker != marker {
				err = fmt.Errorf("unexpected parse from server: %#v from: %s", bres, logbuf.Bytes())
				log.Print(err)
			}
		}
		res.Body.Close()
		if err != nil {
			log.Print(err)
			return nil, err
		}

		blobs = append(blobs, bres.Blobs.Blob...)

		if bres.NextMarker == "" {
			// No more blobs to list
			break
		}
		marker = bres.NextMarker
	}
	return blobs, nil
}

func getAzureError(operation string, res *http.Response) *Error {
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	aerr := &Error{
		Op:     operation,
		Code:   res.StatusCode,
		Body:   body,
		Header: res.Header,
	}
	aerr.parseXML()
	res.Body.Close()
	return aerr
}

// Get retrieves a blob from Azure or returns os.ErrNotExist if not found
func (c *Client) Get(ctx context.Context, container, key string) (body io.ReadCloser, size int64, err error) {
	req := newReq(ctx, c.keyURL(container, key))
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
		return nil, 0, fmt.Errorf("azure HTTP error on GET: %d", res.StatusCode)
	}
}

// GetPartial fetches part of the blob in container.
// If length is negative, the rest of the object is returned.
// The caller must close rc.
func (c *Client) GetPartial(ctx context.Context, container, key string, offset, length int64) (rc io.ReadCloser, err error) {
	if offset < 0 {
		return nil, errors.New("invalid negative length")
	}

	req := newReq(ctx, c.keyURL(container, key))
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
	default:
		res.Body.Close()
		return nil, fmt.Errorf("azure HTTP error on GET: %d", res.StatusCode)
	}
}

// Delete deletes a blob from the specified container.
// It may take a few moments before the blob is actually deleted by Azure.
func (c *Client) Delete(ctx context.Context, container, key string) error {
	req := newReq(ctx, c.keyURL(container, key))
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
		res.StatusCode == http.StatusAccepted {
		return nil
	}
	return fmt.Errorf("azure HTTP error on DELETE: %d", res.StatusCode)
}

// IsValidContainer reports whether container is a valid container name, per Microsoft's naming restrictions.
//
// See http://msdn.microsoft.com/en-us/library/azure/dd135715.aspx
func IsValidContainer(container string) bool {
	l := len(container)
	if l < 3 || l > 63 {
		return false
	}

	valid := false
	prev := byte('-')
	for i := 0; i < len(container); i++ {
		c := container[i]
		switch {
		default:
			return false
		case 'a' <= c && c <= 'z':
			valid = true
		case '0' <= c && c <= '9':
			// Is allowed, but containername can't be just numbers.
			// Therefore, don't set valid to true
		case c == '-':
			if prev == '-' {
				return false
			}
		}
		prev = c
	}

	if prev == '-' {
		return false
	}
	return valid
}

// Error is the type returned by some API operations.
type Error struct {
	Op     string
	Code   int         // HTTP status code
	Body   []byte      // response body
	Header http.Header // response headers

	AzureError XMLError
}

// Error returns a formatted error message
func (e *Error) Error() string {
	if e.AzureError.Code != "" {
		return fmt.Sprintf("azure.%s: status %d, code: %s", e.Op, e.Code, e.AzureError.Code)
	}
	return fmt.Sprintf("azure.%s: status %d", e.Op, e.Code)
}

func (e *Error) parseXML() {
	_ = xml.NewDecoder(bytes.NewReader(e.Body)).Decode(&e.AzureError)
	if e.AzureError.Code == "AuthenticationFailed" {
		log.Printf("Azure AuthenticationFailed. Details: %s", e.AzureError.AuthenticationErrorDetail)
	}

}

// XMLError is the Error response from Azure.
type XMLError struct {
	Code                      string
	Message                   string
	AuthenticationErrorDetail string
}
