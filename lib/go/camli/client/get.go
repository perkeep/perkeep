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
	"camli/blobref"
	"fmt"
	"http"
	"io"
	"os"
)

func (c *Client) newRequest(method, url string) *http.Request {
	req := new(http.Request)
	req.Method = method
	req.ProtoMajor = 1
	req.ProtoMinor = 1
	req.Close = true
	req.Header = http.Header(make(map[string][]string))
	req.URL, _ = http.ParseURL(url)
	req.RawURL = url

	if c.HasAuthCredentials() {
		req.Header.Add("Authorization", c.authHeader())
	}

	return req
}

func (c *Client) Fetch(b *blobref.BlobRef) (blobref.ReadSeekCloser, int64, os.Error) {
	return c.FetchVia(b, nil)
}

func (c *Client) FetchVia(b *blobref.BlobRef, v []*blobref.BlobRef) (blobref.ReadSeekCloser, int64, os.Error) {
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
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}

	if resp.StatusCode != 200 {
		return nil, 0, os.NewError(fmt.Sprintf("Got status code %d from blobserver for %s", resp.StatusCode, b))
	}

	size := resp.ContentLength
	if size == -1 {
		return nil, 0, os.NewError("blobserver didn't return a Content-Length for blob")
	}

	return nopSeeker{resp.Body}, size, nil	
}

type nopSeeker struct {
	io.ReadCloser
}

func (n nopSeeker) Seek(offset int64, whence int) (ret int64, err os.Error) {
	return 0, os.NewError("seek unsupported")
}

