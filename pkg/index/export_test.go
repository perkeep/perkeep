/*
Copyright 2012 The Perkeep Authors

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
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/types/camtypes"
)

func ExpReverseTimeString(s string) string {
	return reverseTimeString(s)
}

func ExpUnreverseTimeString(s string) string {
	return unreverseTimeString(s)
}

func ExpNewCorpus() *Corpus {
	return newCorpus()
}

func (c *Corpus) Exp_mergeFileInfoRow(k, v string) error {
	return c.mergeFileInfoRow([]byte(k), []byte(v))
}

func (c *Corpus) Exp_files(br blob.Ref) camtypes.FileInfo {
	return c.files[br]
}

func ExpKvClaim(k, v string, blobParse func(string) (blob.Ref, bool)) (c camtypes.Claim, ok bool) {
	return kvClaim(k, v, blobParse)
}

func (c *Corpus) SetClaims(pn blob.Ref, claims []*camtypes.Claim) {
	pm := &PermanodeMeta{
		Claims: claims,
	}
	pm.restoreInvariants(c.keyId)
	c.permanodes[pn] = pm
}

func (c *Corpus) Exp_AddKeyID(signerRef blob.Ref, signerID string) error {
	return c.addKeyID(&mutationMap{
		signerID:      signerID,
		signerBlobRef: signerRef,
	})
}

func (x *Index) WithNeededMapsForTest(f func(needs, neededBy map[blob.Ref][]blob.Ref, ready map[blob.Ref]bool)) {
	x.RLock()
	f(x.needs, x.neededBy, x.readyReindex)
	x.RUnlock()
}

func Exp_missingKey(have, missing blob.Ref) string {
	return keyMissing.Key(have, missing)
}

func Exp_schemaVersion() int { return requiredSchemaVersion }

func (x *Index) Exp_noteBlobIndexed(br blob.Ref) {
	x.Lock()
	defer x.Unlock()
	x.noteBlobIndexed(br)
}

func (x *Index) Exp_AwaitReindexing(t *testing.T) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		x.RLock()
		n := len(x.readyReindex)
		x.RUnlock()
		if n == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timeout waiting for readyReindex to drain")
}

func (x *Index) Exp_AwaitAsyncIndexing(t *testing.T) {
	x.reindexWg.Wait()
}

type ExpPnAndTime pnAndTime

// Exp_LSPByTime returns the sorted cache lazySortedPermanodes for
// permanodesByTime (or the reverse sorted one).
func (c *Corpus) Exp_LSPByTime(reverse bool) []ExpPnAndTime {
	if c.permanodesByTime == nil {
		return nil
	}
	var pn []ExpPnAndTime
	if reverse {
		if c.permanodesByTime.sortedCacheReversed != nil {
			for _, v := range c.permanodesByTime.sortedCacheReversed {
				pn = append(pn, ExpPnAndTime(v))
			}
			return pn
		}
	} else {
		if c.permanodesByTime.sortedCache != nil {
			for _, v := range c.permanodesByTime.sortedCache {
				pn = append(pn, ExpPnAndTime(v))
			}
			return pn
		}
	}
	return nil
}

func (x *Index) Exp_BlobSource() blobserver.FetcherEnumerator {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.blobSource
}

func (x *Index) Exp_FixMissingWholeRef(fetcher blob.Fetcher) (err error) {
	return x.fixMissingWholeRef(fetcher)
}

var Exp_ErrMissingWholeRef = errMissingWholeRef

var Exp_KeyRecentPermanode = keyRecentPermanode

func Exp_TypeOfKey(key string) string {
	return typeOfKey(key)
}

func Exp_ClaimsAttrValue(claims []camtypes.Claim, attr string, at time.Time) string {
	return claimsIntfAttrValue(claimSlice(claims), attr, at, nil)
}
