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
	"encoding/json"
	"errors"

	"camlistore.org/pkg/magic"
	"camlistore.org/pkg/schema"
)

// maxSniffSize is how much of a blob to buffer in memory for both
// MIME sniffing (in which case 1MB is way overkill) and also for
// holding a schema blob in memory for analysis in later steps (where
// 1MB is about the max size a claim can be, with about 1023K (of
// slack space)
const maxSniffSize = 1024 * 1024

type BlobSniffer struct {
	header   []byte
	written  int64
	camli    *schema.Superset
	mimeType *string
}

func (sn *BlobSniffer) Superset() (*schema.Superset, bool) {
	return sn.camli, sn.camli != nil
}

func (sn *BlobSniffer) Write(d []byte) (int, error) {
	sn.written += int64(len(d))
	if len(sn.header) < maxSniffSize {
		n := maxSniffSize - len(sn.header)
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
	return sn.written > maxSniffSize
}

func (sn *BlobSniffer) Body() ([]byte, error) {
	if sn.IsTruncated() {
		return nil, errors.New("was truncated")
	}
	return sn.header, nil
}

// returns content type or empty string if unknown
func (sn *BlobSniffer) MimeType() string {
	if sn.mimeType != nil {
		return *sn.mimeType
	}
	return ""
}

func (sn *BlobSniffer) Parse() {
	// Try to parse it as JSON
	// TODO: move this into the magic library?  Is the magic library Camli-specific
	// or to be upstreamed elsewhere?
	if sn.bufferIsCamliJson() {
		str := "application/json; camliType=" + sn.camli.Type
		sn.mimeType = &str
	}

	if mime := magic.MimeType(sn.header); mime != "" {
		sn.mimeType = &mime
	}
}

func (sn *BlobSniffer) bufferIsCamliJson() bool {
	buf := sn.header
	if len(buf) < 2 || buf[0] != '{' {
		return false
	}
	camli := new(schema.Superset)
	err := json.Unmarshal(buf, camli)
	if err != nil {
		return false
	}
	sn.camli = camli
	return true
}
