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

package schema

import (
	"bytes"
	"crypto"
	"fmt"
	"io"
	"os"
	"strings"

	"camli/blobref"
	"camli/blobserver"
)

// WriteFileFromReader creates and uploads a "file" JSON schema
// composed of chunks of r, also uploading the chunks.  The returned
// BlobRef is of the JSON file schema blob.
func WriteFileFromReader(bs blobserver.Storage, filename string, r io.Reader) (*blobref.BlobRef, os.Error) {
	// Naive for now.  Just in 1MB chunks.
	// TODO: rolling hash and hash trees.

	parts := []ContentPart{}
	size, offset := int64(0), int64(0)

	buf := new(bytes.Buffer)
	for {
		buf.Reset()
		offset = size

		n, err := io.Copy(buf, io.LimitReader(r, 1<<20))
		if err != nil {
			return nil, err
		}
		if n == 0 {
			break
		}

		hash := crypto.SHA1.New()
		io.Copy(hash, bytes.NewBuffer(buf.Bytes()))
		br := blobref.FromHash("sha1", hash)
		hasBlob, err := serverHasBlob(bs, br)
		if err != nil {
			return nil, err
		}
		if hasBlob {
			continue
		}

		sb, err := bs.ReceiveBlob(br, buf)
		if err != nil {
			return nil, err
		}
		if expect := (blobref.SizedBlobRef{br, n}); !expect.Equal(sb) {
			return nil, fmt.Errorf("schema/filewriter: wrote %s bytes, got %s ack'd", expect, sb)
		}
		size += n
		parts = append(parts, ContentPart{
			BlobRefString: br.String(),
			BlobRef:       br,
			Size:          uint64(sb.Size),
			Offset:        uint64(offset),
		})
	}

	m := NewCommonFilenameMap(filename)
	err := PopulateRegularFileMap(m, size, parts)
	if err != nil {
		return nil, err
	}

	json, err := MapToCamliJson(m)
	if err != nil {
		return nil, err
	}
	br := blobref.Sha1FromString(json)
	sb, err := bs.ReceiveBlob(br, strings.NewReader(json))
	if err != nil {
		return nil, err
	}
	if expect := (blobref.SizedBlobRef{br, int64(len(json))}); !expect.Equal(sb) {
		return nil, fmt.Errorf("schema/filewriter: wrote %s bytes, got %s ack'd", expect, sb)
	}

	return br, nil
}

func serverHasBlob(bs blobserver.Storage, br *blobref.BlobRef) (have bool, err os.Error) {
	ch := make(chan blobref.SizedBlobRef, 1)
	go func() {
		err = bs.Stat(ch, []*blobref.BlobRef{br}, 0)
		close(ch)
	}()
	for _ = range ch {
		have = true
	}
	return

}
