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
	"crypto/sha1"
	"io"
	"json"
	"log"
	"os"

	mysql "camli/third_party/github.com/Philio/GoMySQL"

	"camli/blobref"
	"camli/blobserver"
	"camli/magic"
	"camli/schema"
)

const maxSniffSize = 1024 * 16

type blobSniffer struct {
	header   []byte
	written  int64
	camli    *schema.Superset
	mimeType *string
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

// returns content type (string) or nil if unknown
func (sn *blobSniffer) MimeType() interface{} {
	if sn.mimeType != nil {
		return *sn.mimeType
	}
	return nil
}

func (sn *blobSniffer) Parse() {
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

func (sn *blobSniffer) bufferIsCamliJson() bool {
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

func (mi *Indexer) ReceiveBlob(blobRef *blobref.BlobRef, source io.Reader) (retsb blobref.SizedBlobRef, err os.Error) {
	sniffer := new(blobSniffer)
	hash := blobRef.Hash()
	var written int64
	written, err = io.Copy(io.MultiWriter(hash, sniffer), source)
	log.Printf("mysqlindexer: hashed+sniffed %d bytes; err %v", written, err)
	if err != nil {
		return
	}

	if !blobRef.HashMatches(hash) {
		err = blobserver.ErrCorruptBlob
		return
	}

	sniffer.Parse()
	mimeType := sniffer.MimeType()
	log.Printf("mysqlindexer: type=%v; truncated=%v", mimeType, sniffer.IsTruncated())

	var client *mysql.Client
	if client, err = mi.getConnection(); err != nil {
		return
	}
	defer mi.releaseConnection(client)

	var stmt *mysql.Statement
	if stmt, err = client.Prepare("INSERT IGNORE INTO blobs (blobref, size, type) VALUES (?, ?, ?)"); err != nil {
		log.Printf("mysqlindexer: prepare error: %v", err)
		return
	}
	if err = stmt.BindParams(blobRef.String(), written, mimeType); err != nil {
		log.Printf("mysqlindexer: bind error: %v", err)
		return
	}
	if err = stmt.Execute(); err != nil {
		log.Printf("mysqlindexer: execute error: %v", err)
		return
	}

	if camli := sniffer.camli; camli != nil {
		switch camli.Type {
		case "claim":
			if err = populateClaim(client, blobRef, camli); err != nil {
				return
			}
		case "permanode":
			if err = populatePermanode(client, blobRef, camli); err != nil {
				return
			}
		case "file":
			if err = mi.populateFile(client, blobRef, camli); err != nil {
				return
			}
		}
	}

	retsb = blobref.SizedBlobRef{BlobRef: blobRef, Size: written}
	return
}

func execSQL(client *mysql.Client, sql string, args ...interface{}) (err os.Error) {
	var stmt *mysql.Statement
	if stmt, err = client.Prepare(sql); err != nil {
		log.Printf("mysqlindexer execSQL prepare: %v", err)
		return
	}
	if err = stmt.BindParams(args...); err != nil {
		log.Printf("mysqlindexer execSQL bind: %v", err)
		return
	}
	if err = stmt.Execute(); err != nil {
		log.Printf("mysqlindexer execSQL exe: %v", err)
		return
	}
	return
}

func populateClaim(client *mysql.Client, blobRef *blobref.BlobRef, camli *schema.Superset) (err os.Error) {
	pnBlobref := blobref.Parse(camli.Permanode)
	if pnBlobref == nil {
		// Skip bogus claim with malformed permanode.
		return
	}

	if err = execSQL(client,
		"INSERT IGNORE INTO claims (blobref, signer, date, unverified, claim, permanode, attr, value) "+
			"VALUES (?, ?, ?, 'Y', ?, ?, ?, ?)",
		blobRef.String(), camli.Signer, camli.ClaimDate,
		camli.ClaimType, camli.Permanode,
		camli.Attribute, camli.Value); err != nil {
		return
	}

	// And update the lastmod on the permanode row.
	if err = execSQL(client,
		"INSERT IGNORE INTO permanodes (blobref) VALUES (?)",
		pnBlobref.String()); err != nil {
		return
	}
	if err = execSQL(client,
		"UPDATE permanodes SET lastmod=? WHERE blobref=? AND ? > lastmod",
		camli.ClaimDate, pnBlobref.String(), camli.ClaimDate); err != nil {
		return
	}

	return nil
}

func populatePermanode(client *mysql.Client, blobRef *blobref.BlobRef, camli *schema.Superset) (err os.Error) {
	err = execSQL(client,
		"INSERT IGNORE INTO permanodes (blobref, unverified, signer, lastmod) "+
			"VALUES (?, 'Y', ?, '')",
		blobRef.String(), camli.Signer)
	return
}

func (mi *Indexer) populateFile(client *mysql.Client, blobRef *blobref.BlobRef, ss *schema.Superset) (err os.Error) {
	if ss.Fragment {
		return nil
	}
	seekFetcher, err := blobref.SeekerFromStreamingFetcher(mi.BlobSource)
	if err != nil {
		return err
	}

	sha1 := sha1.New()
	fr := ss.NewFileReader(seekFetcher)
	n, err := io.Copy(sha1, fr)
	if err != nil {
		return err
	}
	log.Printf("file %s blobref is %s, size %d", blobRef, blobref.FromHash("sha1", sha1), n)
	err = execSQL(client,
		"INSERT IGNORE INTO files (fileschemaref, bytesref, size) VALUES (?, ?, ?)",
		blobRef.String(),
		blobref.FromHash("sha1", sha1).String(),
		n)
	return
}
