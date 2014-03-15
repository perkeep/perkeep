/*
Copyright 2012 Google Inc.

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

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/types/camtypes"
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

func (c *Corpus) SetClaims(pn blob.Ref, claims *PermanodeMeta) {
	c.permanodes[pn] = claims
}

func (x *Index) NeededMapsForTest() (needs, neededBy map[blob.Ref][]blob.Ref, ready map[blob.Ref]bool) {
	return x.needs, x.neededBy, x.readyReindex
}

func Exp_missingKey(have, missing blob.Ref) string {
	return keyMissing.Key(have, missing)
}

func Exp_schemaVersion() int { return requiredSchemaVersion }

func (x *Index) Exp_noteBlobIndexed(br blob.Ref) {
	x.noteBlobIndexed(br)
}

func (x *Index) Exp_AwaitReindexing(t *testing.T) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		x.mu.Lock()
		n := len(x.readyReindex)
		x.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timeout waiting for readyReindex to drain")
}
