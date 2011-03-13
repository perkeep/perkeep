/*
Copyright 2011 Google Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
nYou may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package mysqlindexer

import (
	"camli/blobref"
	"camli/blobserver"

	"bytes"
	"io"
	"json"
	"log"
	"os"

	mysql "github.com/Philio/GoMySQL"
)

const maxSniffSize = 1024 * 16

type blobSniffer struct {
	header  []byte
	written int64
}

func (sn *blobSniffer) Write(d []byte) (int, os.Error) {
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

func (sn *blobSniffer) IsTruncated() bool {
	return sn.written > maxSniffSize
}

type prefixEntry struct {
	prefix []byte
	mtype  string
}

var prefixTable = []prefixEntry{
	{[]byte("\xff\xd8\xff\xe1"), "image/jpeg"},
}

// returns content type (string) or nil if unknown
func (sn *blobSniffer) MimeType() interface{} {
	hlen := len(sn.header)
	for _, pte := range prefixTable {
		plen := len(pte.prefix)
		if hlen > plen && bytes.Equal(sn.header[:plen], pte.prefix) {
			return pte.mtype
		}
	}

	// Try to parse it as JSON
	if m, ok := bufferIsJsonMap(sn.header); ok {
		_, ok := m["camliVersion"]
		if ok {
			if camType, ok := m["camliType"].(string); ok {
				return "application/json; camliType=" + camType
			}
		}
		return "application/json"
	}

	return nil
}

func bufferIsJsonMap(buf []byte) (map[string]interface{}, bool) {
	if len(buf) < 2 || buf[0] != '{' {
		return nil, false
	}
	var got interface{}
	err := json.Unmarshal(buf, &got)
	if err != nil {
		return nil, false
	}
	m, ok := got.(map[string]interface{})
	return m, ok
}

func (mi *Indexer) ReceiveBlob(blobRef *blobref.BlobRef, source io.Reader, mirrorPartions []blobserver.Partition) (retsb *blobref.SizedBlobRef, err os.Error) {
	sniffer := new(blobSniffer)
	hash := blobRef.Hash()
	var written int64
	written, err = io.Copy(io.MultiWriter(hash, sniffer), source)
	log.Printf("mysqlindexer: wrote %d; err %v", written, err)
	if err != nil {
		return
	}

	if !blobRef.HashMatches(hash) {
		err = blobserver.CorruptBlobError
		return
	}

	mimeType := sniffer.MimeType()
	log.Printf("Read %d bytes; type=%v; truncated=%v", written, sniffer.IsTruncated())

	var client *mysql.Client
	if client, err = mi.getConnection(); err != nil {
		return
	}
	defer mi.releaseConnection(client)

	var stmt *mysql.Statement
	if stmt, err = client.Prepare("INSERT INTO blobs (blobref, size, type) VALUES (?, ?, ?)"); err != nil {
		return
	}
	if err = stmt.BindParams(blobRef.String(), written, mimeType); err != nil {
		return
	}
	if err = stmt.Execute(); err != nil {
		return
	}

	retsb = &blobref.SizedBlobRef{BlobRef: blobRef, Size: written}
	return
}
