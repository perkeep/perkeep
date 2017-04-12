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
	"math"
	"net/http"
	"os"
	"regexp"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/constants"
	"camlistore.org/pkg/schema"

	"go4.org/readerutil"
	"go4.org/types"
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
	return c.fetchVia(b, c.viaPathTo(b))
}

func (c *Client) viaPathTo(b blob.Ref) (path []blob.Ref) {
	c.viaMu.RLock()
	defer c.viaMu.RUnlock()
	// Append path backwards first,
	key := b
	for {
		v, ok := c.via[key]
		if !ok {
			break
		}
		key = v
		path = append(path, key)
	}
	// Then reverse it
	for i := 0; i < len(path)/2; i++ {
		path[i], path[len(path)-i-1] = path[len(path)-i-1], path[i]
	}
	return
}

var blobsRx = regexp.MustCompile(blob.Pattern)

func (c *Client) fetchVia(b blob.Ref, v []blob.Ref) (body io.ReadCloser, size uint32, err error) {
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

	var reader io.Reader = resp.Body
	var closer io.Closer = resp.Body
	if resp.ContentLength > 0 {
		if resp.ContentLength > math.MaxUint32 {
			return nil, 0, fmt.Errorf("Blob %s over %d bytes", b, uint32(math.MaxUint32))
		}
		size = uint32(resp.ContentLength)
	} else {
		var buf bytes.Buffer
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

	var buf bytes.Buffer
	if err := c.UpdateShareChain(b, io.TeeReader(reader, &buf)); err != nil {
		if err != ErrNotSharing {
			return nil, 0, err
		}
	}
	mr := io.MultiReader(&buf, reader)
	var rc io.ReadCloser = struct {
		io.Reader
		io.Closer
	}{mr, closer}

	return rc, size, nil
}

// ErrNotSharing is returned when a client that was not created with
// NewFromShareRoot tries to access shared blobs.
var ErrNotSharing = errors.New("Client can not deal with shared blobs. Create it with NewFromShareRoot.")

// UpdateShareChain reads the schema of b from r, and instructs the client that
// all blob refs found in this schema should use b as a preceding chain link, in
// all subsequent shared blobs fetches. If the client was not created with
// NewFromShareRoot, ErrNotSharing is returned.
func (c *Client) UpdateShareChain(b blob.Ref, r io.Reader) error {
	c.viaMu.Lock()
	defer c.viaMu.Unlock()
	if c.via == nil {
		// Not in sharing mode, so return immediately.
		return ErrNotSharing
	}
	// Slurp 1 MB to find references to other blobrefs for the via path.
	var buf bytes.Buffer
	const maxSlurp = 1 << 20
	if _, err := io.Copy(&buf, io.LimitReader(r, maxSlurp)); err != nil {
		return err
	}
	// If it looks like a JSON schema blob (starts with '{')
	if schema.LikelySchemaBlob(buf.Bytes()) {
		for _, blobstr := range blobsRx.FindAllString(buf.String(), -1) {
			br, ok := blob.Parse(blobstr)
			if !ok {
				log.Printf("Invalid blob ref %q noticed in schema of %v", blobstr, b)
				continue
			}
			c.via[br] = b
		}
	}
	return nil
}

func (c *Client) ReceiveBlob(br blob.Ref, source io.Reader) (blob.SizedRef, error) {
	if c.sto != nil {
		return blobserver.Receive(c.sto, br, source)
	}
	size, ok := readerutil.Size(source)
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
