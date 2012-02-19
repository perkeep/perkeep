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
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"

	"camlistore.org/pkg/blobref"
)

var _ = log.Printf

func (c *Client) FetchStreaming(b *blobref.BlobRef) (io.ReadCloser, int64, error) {
	return c.FetchVia(b, nil)
}

func (c *Client) FetchVia(b *blobref.BlobRef, v []*blobref.BlobRef) (io.ReadCloser, int64, error) {
	url := fmt.Sprintf("%s/camli/%s", c.server, b)

	if len(v) > 0 {
		buf := bytes.NewBufferString(url)
		buf.WriteString("?via=")
		for i, br := range v {
			if i != 0 {
				buf.WriteString(",")
			}
			buf.WriteString(br.String())
		}
		url = buf.String()
	}

	req := c.newRequest("GET", url)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}

	if resp.StatusCode != 200 {
		return nil, 0, errors.New(fmt.Sprintf("Got status code %d from blobserver for %s", resp.StatusCode, b))
	}

	size := resp.ContentLength
	if size == -1 {
		return nil, 0, errors.New("blobserver didn't return a Content-Length for blob")
	}

	return resp.Body, size, nil
}
