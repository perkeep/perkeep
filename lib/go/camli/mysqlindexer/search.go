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
	if len(owner) > 1 {
		panic("TODO: remove support for more than one owner. push it to caller")
	}

	rs, err := mi.db.Query("SELECT blobref, signer, lastmod FROM permanodes WHERE signer = ? AND lastmod <> '' "+
		"ORDER BY lastmod DESC LIMIT ?",
		owner[0].String(), limit)
	if err != nil {
		return err
	}
	defer rs.Close()

	var blobstr, signerstr, modstr string
	for rs.Next() {
		if err := rs.Scan(&blobstr, &signerstr, &modstr); err != nil {
			return err
		}
		br := blobref.Parse(blobstr)
		if br == nil {
			continue
		}
		signer := blobref.Parse(signerstr)
		if signer == nil {
			continue
		}
		modstr = trimRFC3339Subseconds(modstr)
		t, err := time.Parse(time.RFC3339, modstr)
		if err != nil {
			log.Printf("Skipping; error parsing time %q: %v", modstr, err)
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

func (mi *Indexer) GetOwnerClaims(permanode, owner *blobref.BlobRef) (claims search.ClaimList, err os.Error) {
	claims = make(search.ClaimList, 0)

	// TODO: ignore rows where unverified = 'N'
	rs, err := mi.db.Query("SELECT blobref, date, claim, attr, value FROM claims WHERE permanode = ? AND signer = ?",
		permanode.String(), owner.String())
	if err != nil {
		return
	}
	defer rs.Close()

	var row claimsRow
	for rs.Next() {
		err = rs.Scan(&row.blobref, &row.date, &row.claim, &row.attr, &row.value)
		if err != nil {
			return
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
	rs, err := mi.db.Query("SELECT type, size FROM blobs WHERE blobref=?", blob.String())
	if err != nil {
		return
	}
	defer rs.Close()
	if !rs.Next() {
		err = os.ENOENT
		return
	}
	err = rs.Scan(&mime, &size)
	return
}

func (mi *Indexer) SearchPermanodesWithAttr(dest chan<- *blobref.BlobRef, request *search.PermanodeByAttrRequest) os.Error {
	defer close(dest)
	keyId, err := mi.keyIdOfSigner(request.Signer)
	if err != nil {
		return err
	}
	query := ""
	var rs ResultSet
	if request.Attribute == "" {
		query = "SELECT permanode FROM signerattrvalueft WHERE keyid = ? AND MATCH(value) AGAINST (?) AND claimdate <> '' LIMIT ?"
		rs, err = mi.db.Query(query, keyId, request.Query, request.MaxResults)
		if err != nil {
			return err
		}
	} else {
		if request.FuzzyMatch {
			query = "SELECT permanode FROM signerattrvalueft WHERE keyid = ? AND attr = ? AND MATCH(value) AGAINST (?) AND claimdate <> '' LIMIT ?"
			rs, err = mi.db.Query(query, keyId, request.Attribute,
				request.Query, request.MaxResults)
			if err != nil {
				return err
			}
		} else {
			query = "SELECT permanode FROM signerattrvalue WHERE keyid = ? AND attr = ? AND value = ? AND claimdate <> '' ORDER BY claimdate DESC LIMIT ?"
			rs, err = mi.db.Query(query, keyId, request.Attribute,
				request.Query, request.MaxResults)
			if err != nil {
				return err
			}
		}
	}
	defer rs.Close()

	pn := ""
	for rs.Next() {
		if err := rs.Scan(&pn); err != nil {
			return err
		}
		br := blobref.Parse(pn)
		if br == nil {
			continue
		}
		dest <- br
	}
	return nil
}

func (mi *Indexer) ExistingFileSchemas(bytesRef *blobref.BlobRef) (files []*blobref.BlobRef, err os.Error) {
	rs, err := mi.db.Query("SELECT fileschemaref FROM files WHERE bytesref=?", bytesRef.String())
	if err != nil {
		return
	}
	defer rs.Close()

	ref := ""
	for rs.Next() {
		if err := rs.Scan(&ref); err != nil {
			return nil, err
		}
		files = append(files, blobref.Parse(ref))
	}
	return
}

func (mi *Indexer) GetFileInfo(fileRef *blobref.BlobRef) (*search.FileInfo, os.Error) {
	rs, err := mi.db.Query("SELECT size, filename, mime FROM files WHERE fileschemaref=?",
		fileRef.String())
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	if !rs.Next() {
		return nil, os.ENOENT
	}
	var fi search.FileInfo
	err = rs.Scan(&fi.Size, &fi.FileName, &fi.MimeType)
	return &fi, err
}

func (mi *Indexer) keyIdOfSigner(signer *blobref.BlobRef) (keyid string, err os.Error) {
	rs, err := mi.db.Query("SELECT keyid FROM signerkeyid WHERE blobref=?", signer.String())
	if err != nil {
		return
	}
	defer rs.Close()

	if !rs.Next() {
		return "", fmt.Errorf("mysqlindexer: failed to find keyid of signer %q", signer.String())
	}
	err = rs.Scan(&keyid)
	return
}

func (mi *Indexer) PermanodeOfSignerAttrValue(signer *blobref.BlobRef, attr, val string) (permanode *blobref.BlobRef, err os.Error) {
	keyId, err := mi.keyIdOfSigner(signer)
	if err != nil {
		return nil, err
	}

	rs, err := mi.db.Query("SELECT permanode FROM signerattrvalue WHERE keyid=? AND attr=? AND value=? ORDER BY claimdate DESC LIMIT 1",
		keyId, attr, val)
	if err != nil {
		return
	}
	defer rs.Close()

	if !rs.Next() {
		return nil, os.NewError("mysqlindexer: no signerattrvalue match")
	}
	var blobstr string
	if err = rs.Scan(&blobstr); err != nil {
		return
	}
	return blobref.Parse(blobstr), nil
}

func (mi *Indexer) PathsOfSignerTarget(signer, target *blobref.BlobRef) (paths []*search.Path, err os.Error) {
	keyId, err := mi.keyIdOfSigner(signer)
	if err != nil {
		return
	}

	rs, err := mi.db.Query("SELECT claimref, claimdate, baseref, suffix, active FROM path WHERE keyid=? AND targetref=?",
		keyId, target.String())
	if err != nil {
		return
	}
	defer rs.Close()

	mostRecent := make(map[string]*search.Path)
	maxClaimDates := make(map[string]string)
	var claimRef, claimDate, baseRef, suffix, active string
	for rs.Next() {
		if err = rs.Scan(&claimRef, &claimDate, &baseRef, &suffix, &active); err != nil {
			return
		}

		key := baseRef + "/" + suffix

		if claimDate > maxClaimDates[key] {
			maxClaimDates[key] = claimDate
			if active == "Y" {
				mostRecent[key] = &search.Path{
					Claim:     blobref.MustParse(claimRef),
					ClaimDate: claimDate,
					Base:      blobref.MustParse(baseRef),
					Suffix:    suffix,
				}
			} else {
				mostRecent[key] = nil, false
			}
		}
	}
	paths = make([]*search.Path, 0)
	for _, v := range mostRecent {
		paths = append(paths, v)
	}
	return paths, nil
}

func (mi *Indexer) PathLookup(signer, base *blobref.BlobRef, suffix string, at *time.Time) (*search.Path, os.Error) {
	// TODO: pass along the at time to a new helper function to
	// filter? maybe not worth it, since this list should be
	// small.
	paths, err := mi.PathsLookup(signer, base, suffix)
	if err != nil {
		return nil, err
	}
	var (
		newest    = int64(0)
		atSeconds = int64(0)
		best      *search.Path
	)
	if at != nil {
		atSeconds = at.Seconds()
	}
	for _, path := range paths {
		t, err := time.Parse(time.RFC3339, trimRFC3339Subseconds(path.ClaimDate))
		if err != nil {
			continue
		}
		secs := t.Seconds()
		if atSeconds != 0 && secs > atSeconds {
			// Too new
			continue
		}
		if newest > secs {
			// Too old
			continue
		}
		// Just right
		newest, best = secs, path
	}
	if best == nil {
		return nil, os.ENOENT
	}
	return best, nil
}

func (mi *Indexer) PathsLookup(signer, base *blobref.BlobRef, suffix string) (paths []*search.Path, err os.Error) {
	keyId, err := mi.keyIdOfSigner(signer)
	if err != nil {
		return
	}
	rs, err := mi.db.Query("SELECT claimref, claimdate, targetref FROM path "+
		"WHERE keyid=? AND baseref=? AND suffix=?",
		keyId, base.String(), suffix)
	if err != nil {
		return
	}
	defer rs.Close()

	var claimref, claimdate, targetref string
	for rs.Next() {
		if err = rs.Scan(&claimref, &claimdate, &targetref); err != nil {
			return
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
