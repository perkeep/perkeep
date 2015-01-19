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
	"math"
	"net/http"
	"os"
	"regexp"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/constants"
	"camlistore.org/pkg/readerutil"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/types"
)

func (c *Client) FetchSchemaBlob(b blob.Ref) (*schema.Blob, error) {
	rc, _, err := c.Fetch(b)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return schema.BlobFromReader(b, rc)
}

func (c *Client) Fetch(b blob.Ref) (io.ReadCloser, uint32, error) {
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

func (c *Client) FetchVia(b blob.Ref, v []blob.Ref) (body io.ReadCloser, size uint32, err error) {
	if c.sto != nil {
		if len(v) > 0 {
			return nil, 0, errors.New("FetchVia not supported in non-HTTP mode")
		}
		return c.sto.Fetch(b)
	}
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
	if resp.StatusCode == http.StatusNotFound {
		// Per blob.Fetcher contract:
		return nil, 0, os.ErrNotExist
	}
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("Got status code %d from blobserver for %s", resp.StatusCode, b)
	}

	var buf bytes.Buffer
	var reader io.Reader = io.MultiReader(&buf, resp.Body)
	var closer io.Closer = resp.Body
	if resp.ContentLength > 0 {
		if resp.ContentLength > math.MaxUint32 {
			return nil, 0, fmt.Errorf("Blob %s over %d bytes", b, uint32(math.MaxUint32))
		}
		size = uint32(resp.ContentLength)
	} else {
		size = 0
		// Might be compressed. Slurp it to memory.
		n, err := io.CopyN(&buf, resp.Body, constants.MaxBlobSize+1)
		if n > blobserver.MaxBlobSize {
			return nil, 0, fmt.Errorf("Blob %s over %d bytes; not reading more", b, blobserver.MaxBlobSize)
		}
		if err == nil {
			panic("unexpected")
		} else if err == io.EOF {
			size = uint32(n)
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
	if c.sto != nil {
		return blobserver.Receive(c.sto, br, source)
	}
	size, ok := readerutil.ReaderSize(source)
	if !ok {
		size = 0
	}
	h := &UploadHandle{
		BlobRef:  br,
		Size:     uint32(size), // 0 if we don't know
		Contents: source,
		SkipStat: true,
	}
	pr, err := c.Upload(h)
	if err != nil {
		return blob.SizedRef{}, err
	}
	return pr.SizedBlobRef(), nil
}
