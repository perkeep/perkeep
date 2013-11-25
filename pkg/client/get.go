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
	"fmt"
	"io"
	"regexp"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/readerutil"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/types"
)

func (c *Client) FetchSchemaBlob(b blob.Ref) (*schema.Blob, error) {
	rc, _, err := c.FetchStreaming(b)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return schema.BlobFromReader(b, rc)
}

func (c *Client) FetchStreaming(b blob.Ref) (io.ReadCloser, int64, error) {
	return c.FetchVia(b, c.viaPathTo(b))
}

func (c *Client) viaPathTo(b blob.Ref) (path []blob.Ref) {
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
		path = append(path, blob.MustParse(v))
		it = v
	}
	// Then reverse it
	for i := 0; i < len(path)/2; i++ {
		path[i], path[len(path)-i-1] = path[len(path)-i-1], path[i]
	}
	return
}

var blobsRx = regexp.MustCompile(blob.Pattern)

func (c *Client) FetchVia(b blob.Ref, v []blob.Ref) (body io.ReadCloser, size int64, err error) {
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
	defer func() {
		if err != nil {
			resp.Body.Close()
		}
	}()

	if resp.StatusCode != 200 {
		return nil, 0, fmt.Errorf("Got status code %d from blobserver for %s", resp.StatusCode, b)
	}

	var buf bytes.Buffer
	var reader io.Reader = io.MultiReader(&buf, resp.Body)
	var closer io.Closer = resp.Body
	size = resp.ContentLength
	if size == -1 {
		// Might be compressed. Slurp it to memory.
		n, err := io.CopyN(&buf, resp.Body, blobserver.MaxBlobSize+1)
		if n > blobserver.MaxBlobSize {
			return nil, 0, fmt.Errorf("Blob %b over %d bytes; not reading more", b, blobserver.MaxBlobSize)
		}
		if err == nil {
			panic("unexpected")
		} else if err == io.EOF {
			size = n
			reader, closer = &buf, types.NopCloser
		} else {
			return nil, 0, fmt.Errorf("Error reading %s: %v", b, err)
		}
	}

	var rc io.ReadCloser = struct {
		io.Reader
		io.Closer
	}{reader, closer}

	if c.via == nil {
		// Not in sharing mode, so return immediately.
		return rc, size, nil
	}

	// Slurp 1 MB to find references to other blobrefs for the via path.
	if buf.Len() == 0 {
		const maxSlurp = 1 << 20
		_, err = io.Copy(&buf, io.LimitReader(resp.Body, maxSlurp))
		if err != nil {
			return nil, 0, err
		}
	}
	// If it looks like a JSON schema blob (starts with '{')
	if schema.LikelySchemaBlob(buf.Bytes()) {
		for _, blobstr := range blobsRx.FindAllString(buf.String(), -1) {
			c.via[blobstr] = b.String()
		}
	}
	return rc, size, nil
}

func (c *Client) ReceiveBlob(br blob.Ref, source io.Reader) (blob.SizedRef, error) {
	size, ok := readerutil.ReaderSize(source)
	if !ok {
		size = -1
	}
	h := &UploadHandle{
		BlobRef:  br,
		Size:     size, // -1 if we don't know
		Contents: source,
	}
	pr, err := c.Upload(h)
	if err != nil {
		return blob.SizedRef{}, err
	}
	return pr.SizedBlobRef(), nil
}
