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
	"camli/search"

	"log"
	"os"
	"strings"
	"time"
)

type permaNodeRow struct {
	blobref string
	signer  string
	lastmod string // "2011-03-13T23:30:19.03946Z"
}

func (mi *Indexer) GetRecentPermanodes(dest chan *search.Result, owner []*blobref.BlobRef, limit int) os.Error {
	defer close(dest)
	if len(owner) == 0 {
		return nil
	}

	// TODO: support multiple
	user := owner[0]

	client, err := mi.getConnection()
	if err != nil {
		return err
	}
	defer mi.releaseConnection(client)

	stmt, err := client.Prepare("SELECT blobref, signer, lastmod FROM permanodes WHERE signer = ? AND lastmod <> '' ORDER BY lastmod DESC LIMIT ?")
	if err != nil {
		return err
	}
	err = stmt.BindParams(user.String(), limit) // TODO: more than one owner, verification
	if err != nil {
		return err
	}
	err = stmt.Execute()
	if err != nil {
		return err
	}

	var row permaNodeRow
	stmt.BindResult(&row.blobref, &row.signer, &row.lastmod)
	for {
		done, err := stmt.Fetch()
		if err != nil {
			return err
		}
		if done {
			break
		}
		br := blobref.Parse(row.blobref)
		if br == nil {
			continue
		}
		signer := blobref.Parse(row.signer)
		if signer == nil {
			continue
		}
		row.lastmod = trimRFC3339Subseconds(row.lastmod)
		t, err := time.Parse(time.RFC3339, row.lastmod)
		if err != nil {
			log.Printf("Skipping; error parsing time %q: %v", row.lastmod, err)
			continue
		}
		dest <- &search.Result{
			BlobRef:     br,
			Signer:      signer,
			LastModTime: t.Seconds(),
		}
	}

	return nil
}

func trimRFC3339Subseconds(s string) string {
	if !strings.HasSuffix(s, "Z") || len(s) < 20 || s[19] != '.' {
		return s
	}
	return s[:19] + "Z"
}

type claimsRow struct {
	blobref, signer, date, claim, unverified, permanode, attr, value string
}

func (mi *Indexer) GetOwnerClaims(permanode, owner *blobref.BlobRef) (claims search.ClaimList, reterr os.Error) {
	claims = make(search.ClaimList, 0)
	client, err := mi.getConnection()
	if err != nil {
		reterr = err
		return
	}
	defer mi.releaseConnection(client)

	// TODO: ignore rows where unverified = 'N'
	stmt, err := client.Prepare("SELECT blobref, date, claim, attr, value FROM claims WHERE permanode = ? AND signer = ?")
	if err != nil {
		reterr = err
		return
	}
	err = stmt.BindParams(permanode.String(), owner.String())
	if err != nil {
		reterr = err
		return
	}
	err = stmt.Execute()
	if err != nil {
		reterr = err
		return
	}

	var row claimsRow
	stmt.BindResult(&row.blobref, &row.date, &row.claim, &row.attr, &row.value)
	defer stmt.Close()
	for {
		done, err := stmt.Fetch()
		if err != nil {
			reterr = err
			return
		}
		if done {
			break
		}
		t, err := time.Parse(time.RFC3339, trimRFC3339Subseconds(row.date))
		if err != nil {
			log.Printf("Skipping; error parsing time %q: %v", row.date, err)
			continue
		}
		claims = append(claims, &search.Claim{
			BlobRef:   blobref.Parse(row.blobref),
			Signer:    owner,
			Permanode: permanode,
			Type:      row.claim,
			Date:      t,
			Attr:      row.attr,
			Value:     row.value,
		})
	}
	return
}

func (mi *Indexer) GetBlobMimeType(blob *blobref.BlobRef) (mime string, size int64, reterr os.Error) {
	client, err := mi.getConnection()
	if err != nil {
		reterr = err
		return
	}
	defer mi.releaseConnection(client)

	stmt, err := client.Prepare("SELECT type, size FROM blobs WHERE blobref=?")
	if err != nil {
		reterr = err
		return
	}
	err = stmt.BindParams(blob.String())
	if err != nil {
		reterr = err
		return
	}
	err = stmt.Execute()
	if err != nil {
		reterr = err
		return
	}

	stmt.BindResult(&mime, &size)
	defer stmt.Close()
	ok := false
	for {
		done, err := stmt.Fetch()
		if err != nil {
			reterr = err
			return
		}
		ok = true
		if done {
			break
		}
	}
	if !ok && err == nil {
		err = os.ENOENT
	}
	return
}