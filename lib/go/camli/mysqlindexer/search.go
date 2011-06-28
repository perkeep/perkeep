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
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"camli/blobref"
	"camli/search"
)

// Statically verify that Indexer implements the search.Index interface.
var _ search.Index = (*Indexer)(nil)

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

func (mi *Indexer) GetBlobMimeType(blob *blobref.BlobRef) (mime string, size int64, err os.Error) {
	client, err := mi.getConnection()
	if err != nil {
		return
	}
	defer func() {
		if err == nil || err == os.ENOENT {
			mi.releaseConnection(client)
		} else {
			client.Close()
		}
	}()

	err = client.Query(fmt.Sprintf("SELECT type, size FROM blobs WHERE blobref=%q", blob.String()))
	if err != nil {
		return
	}

	result, err := client.StoreResult()
	if err != nil {
		return
	}
	defer client.FreeResult()

	row := result.FetchRow()
	if row == nil {
		err = os.ENOENT
		return
	}

	//log.Printf("got row: %#v (2 is %T)", row, row[1])
	mime, _ = row[0].(string)
	size, _ = row[1].(int64)
	return
}

func (mi *Indexer) ExistingFileSchemas(bytesRef *blobref.BlobRef) (files []*blobref.BlobRef, err os.Error) {
	client, err := mi.getConnection()
	if err != nil {
		return
	}
	defer func() {
		if err == nil {
			mi.releaseConnection(client)
		} else {
			client.Close()
		}
	}()

	err = client.Query(fmt.Sprintf("SELECT fileschemaref FROM files WHERE bytesref=%q", bytesRef.String()))
	if err != nil {
		return
	}

	result, err := client.StoreResult()
	if err != nil {
		return
	}
	defer client.FreeResult()

	for {
		row := result.FetchRow()
		if row == nil {
			break
		}
		files = append(files, blobref.Parse(row[0].(string)))
	}
	return
}

func (mi *Indexer) GetFileInfo(fileRef *blobref.BlobRef) (fi *search.FileInfo, err os.Error) {
	client, err := mi.getConnection()
	if err != nil {
		return
	}
	defer func() {
		if err == nil {
			mi.releaseConnection(client)
		} else {
			client.Close()
		}
	}()

	err = client.Query(fmt.Sprintf("SELECT size, filename, mime FROM files WHERE fileschemaref=%q", fileRef.String()))
	if err != nil {
		return
	}

	result, err := client.StoreResult()
	if err != nil {
		return
	}
	defer client.FreeResult()

	row := result.FetchRow()
	if row == nil {
		return nil, os.ENOENT
	}

	size, _ := row[0].(int64)
	fileName, _ := row[1].(string)
	mimeType, _ := row[2].(string)

	return &search.FileInfo{
		Size:     size,
		FileName: fileName,
		MimeType: mimeType,
	}, nil
}

func (mi *Indexer) keyIdOfSigner(signer *blobref.BlobRef) (keyid string, err os.Error) {
	client, err := mi.getConnection()
	if err != nil {
		return
	}
	defer func() {
		if err == nil {
			mi.releaseConnection(client)
		} else {
			client.Close()
		}
	}()

	err = client.Query(fmt.Sprintf("SELECT keyid FROM signerkeyid WHERE blobref=%q", signer.String()))
	if err != nil {
		return
	}

	result, err := client.StoreResult()
	if err != nil {
		return
	}
	defer client.FreeResult()

	row := result.FetchRow()
	if row == nil {
		return "", fmt.Errorf("mysqlindexer: failed to find keyid of signer %q", signer.String())
	}

	return row[0].(string), nil
}

func (mi *Indexer) PermanodeOfSignerAttrValue(signer *blobref.BlobRef, attr, val string) (permanode *blobref.BlobRef, err os.Error) {
	keyId, err := mi.keyIdOfSigner(signer)
	if err != nil {
		return nil, err
	}

	client, err := mi.getConnection()
	if err != nil {
		return
	}
	defer func() {
		if err == nil {
			mi.releaseConnection(client)
		} else {
			client.Close()
		}
	}()

	// TODO: lame %q quoting not SQL compatible; should use SQL ? bind params
	err = client.Query(fmt.Sprintf("SELECT permanode FROM signerattrvalue WHERE keyid=%q AND attr=%q AND value=%q ORDER BY claimdate DESC LIMIT 1",
		keyId, attr, val))
	if err != nil {
		return
	}

	result, err := client.StoreResult()
	if err != nil {
		return
	}
	defer client.FreeResult()

	row := result.FetchRow()
	if row == nil {
		return nil, os.NewError("mysqlindexer: no signerattrvalue match")
	}
	return blobref.Parse(row[0].(string)), nil
}

func (mi *Indexer) PathsOfSignerTarget(signer, target *blobref.BlobRef) (paths []*search.Path, err os.Error) {
	keyId, err := mi.keyIdOfSigner(signer)
	if err != nil {
		return
	}

	client, err := mi.getConnection()
	if err != nil {
		return
	}
	defer mi.releaseConnection(client)

	// TODO: lame %q quoting not SQL compatible; should use SQL ? bind params
	err = client.Query(fmt.Sprintf("SELECT claimref, claimdate, baseref, suffix FROM path WHERE keyid=%q AND targetref=%q",
		keyId, target.String()))
	if err != nil {
		return
	}

	result, err := client.StoreResult()
	if err != nil {
		return
	}
	defer client.FreeResult()

	mostRecent := make(map[string]*search.Path)
	for {
		row := result.FetchRow()
		if row == nil {
			break
		}
		claimRef, claimDate, baseRef, suffix :=
			blobref.MustParse(row[0].(string)), row[1].(string),
			blobref.MustParse(row[2].(string)), row[3].(string)
		key := baseRef.String() + "/" + suffix
		best, ok := mostRecent[key]
		if !ok || claimDate > best.ClaimDate {
			mostRecent[key] = &search.Path{
				Claim:     claimRef,
				ClaimDate: claimDate,
				Base:      baseRef,
				Suffix:    suffix,
			}
		}
	}
	paths = make([]*search.Path, 0)
	for _, v := range mostRecent {
		paths = append(paths, v)
	}
	return paths, nil
}

func (mi *Indexer) PathLookup(signer, base *blobref.BlobRef, suffix string) (paths []*search.Path, err os.Error) {
	keyId, err := mi.keyIdOfSigner(signer)
	if err != nil {
		return
	}
	client, err := mi.getConnection()
	if err != nil {
		return
	}
	defer mi.releaseConnection(client)

	stmt, err := client.Prepare("SELECT claimref, claimdate, targetref FROM path " +
		"WHERE keyid=? AND baseref=? AND suffix=?")
	if err != nil {
		return
	}

	err = stmt.BindParams(keyId, base.String(), suffix)
	if err != nil {
		return
	}

	err = stmt.Execute()
	if err != nil {
		return
	}

	var claimref, claimdate, targetref string
	stmt.BindResult(&claimref, &claimdate, &targetref)
	defer stmt.Close()
	for {
		done, ferr := stmt.Fetch()
		if ferr != nil {
			err = ferr
			return
		}
		if done {
			break
		}
		t, err := time.Parse(time.RFC3339, trimRFC3339Subseconds(claimdate))
		if err != nil {
			log.Printf("Skipping bogus path row with bad time: %q", claimref)
			continue
		}
		_ = t // TODO: use this?
		paths = append(paths, &search.Path{
			Claim:     blobref.Parse(claimref),
			ClaimDate: claimdate,
			Base:      base,
			Target:    blobref.Parse(targetref),
			Suffix:    suffix,
		})
	}
	return
}
