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
	"regexp"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/readerutil"
	"camlistore.org/pkg/schema"
)

var _ = log.Printf

func (c *Client) FetchSchemaBlob(b *blobref.BlobRef) (*schema.Blob, error) {
	rc, _, err := c.FetchStreaming(b)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return schema.BlobFromReader(b, rc)
}

func (c *Client) FetchStreaming(b *blobref.BlobRef) (io.ReadCloser, int64, error) {
	return c.FetchVia(b, c.viaPathTo(b))
}

func (c *Client) viaPathTo(b *blobref.BlobRef) (path []*blobref.BlobRef) {
	if c.via == nil {
		return nil
	}
	it := b.String()
	// Append path backwards first,
	for {
		v := c.via[it]
		if v == "" {
			break
		}
		path = append(path, blobref.MustParse(v))
		it = v
	}
	// Then reverse it
	for i := 0; i < len(path)/2; i++ {
		path[i], path[len(path)-i-1] = path[len(path)-i-1], path[i]
	}
	return
}

var blobsRx = regexp.MustCompile(blobref.Pattern)

func (c *Client) FetchVia(b *blobref.BlobRef, v []*blobref.BlobRef) (io.ReadCloser, int64, error) {
	pfx, err := c.blobPrefix()
	if err != nil {
		return nil, 0, err
	}
	url := fmt.Sprintf("%s/%s", pfx, b)

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

	if c.via == nil {
		// Not in sharing mode, so return immediately.
		return resp.Body, size, nil
	}

	// Slurp 1 MB to find references to other blobrefs for the via path.
	const maxSlurp = 1 << 20
	var buf bytes.Buffer
	_, err = io.Copy(&buf, io.LimitReader(resp.Body, maxSlurp))
	if err != nil {
		return nil, 0, err
	}
	// If it looks like a JSON schema blob (starts with '{')
	if schema.LikelySchemaBlob(buf.Bytes()) {
		for _, blobstr := range blobsRx.FindAllString(buf.String(), -1) {
			c.via[blobstr] = b.String()
		}
	}
	// Read from the multireader, but close the HTTP response body.
	type rc struct {
		io.Reader
		io.Closer
	}
	return rc{io.MultiReader(&buf, resp.Body), resp.Body}, size, nil
}

func (c *Client) ReceiveBlob(blob *blobref.BlobRef, source io.Reader) (blobref.SizedBlobRef, error) {
	size, ok := readerutil.ReaderSize(source)
	if !ok {
		size = -1
	}
	h := &UploadHandle{
		BlobRef:  blob,
		Size:     size, // -1 if we don't know
		Contents: source,
	}
	pr, err := c.Upload(h)
	if err != nil {
		return blobref.SizedBlobRef{}, err
	}
	return pr.SizedBlobRef(), nil
}
