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

package index

import (
	"bytes"
	"errors"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/magic"
	"camlistore.org/pkg/schema"
)

type BlobSniffer struct {
	br blob.Ref

	header    []byte
	written   int64
	meta      *schema.Blob // or nil
	mimeType  string
	camliType string
}

func NewBlobSniffer(ref blob.Ref) *BlobSniffer {
	if !ref.Valid() {
		panic("invalid ref")
	}
	return &BlobSniffer{br: ref}
}

func (sn *BlobSniffer) SchemaBlob() (meta *schema.Blob, ok bool) {
	return sn.meta, sn.meta != nil
}

func (sn *BlobSniffer) Write(d []byte) (int, error) {
	if !sn.br.Valid() {
		panic("write on sniffer with invalid blobref")
	}
	sn.written += int64(len(d))
	if len(sn.header) < schema.MaxSchemaBlobSize {
		n := schema.MaxSchemaBlobSize - len(sn.header)
		if len(d) < n {
			n = len(d)
		}
		sn.header = append(sn.header, d[:n]...)
	}
	return len(d), nil
}

func (sn *BlobSniffer) Size() int64 {
	return sn.written
}

func (sn *BlobSniffer) IsTruncated() bool {
	return sn.written > schema.MaxSchemaBlobSize
}

func (sn *BlobSniffer) Body() ([]byte, error) {
	if sn.IsTruncated() {
		return nil, errors.New("index.Body: was truncated")
	}
	return sn.header, nil
}

// MIMEType returns the sniffed blob's content-type or the empty string if unknown.
// If the blob is a Camlistore schema metadata blob, the MIME type will be of
// the form "application/json; camliType=foo".
func (sn *BlobSniffer) MIMEType() string { return sn.mimeType }

func (sn *BlobSniffer) CamliType() string { return sn.camliType }

func (sn *BlobSniffer) Parse() {
	if sn.bufferIsCamliJSON() {
		sn.camliType = sn.meta.Type()
		sn.mimeType = "application/json; camliType=" + sn.camliType
	} else {
		sn.mimeType = magic.MIMEType(sn.header)
	}
}

func (sn *BlobSniffer) bufferIsCamliJSON() bool {
	buf := sn.header
	if !schema.LikelySchemaBlob(buf) {
		return false
	}
	blob, err := schema.BlobFromReader(sn.br, bytes.NewReader(buf))
	if err != nil {
		return false
	}
	sn.meta = blob
	return true
}
