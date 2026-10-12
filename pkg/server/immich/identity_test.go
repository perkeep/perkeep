/*
Copyright 2026 The Perkeep Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package immich

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"perkeep.org/pkg/blob"
)

func TestImmichAssetID(t *testing.T) {
	owner := blob.MustParse("sha224-" + strings.Repeat("1", 56))
	whole := blob.MustParse("sha224-" + strings.Repeat("2", 56))
	const want = "996b52e4-0765-4a9f-b91e-ccdc3a9f9f43"
	if got := immichAssetID(owner, whole); got != want {
		t.Fatalf("asset ID = %q, want fixed derivation %q", got, want)
	}
	tests := []struct {
		name         string
		owner, whole blob.Ref
	}{
		{"same inputs", owner, whole},
		{"different owner", blob.RefFromString("other signing public key"), whole},
		{"different original", owner, blob.RefFromString("other original")},
		{"owner hash algorithm", blob.MustParse("sha1-" + strings.Repeat("1", 40)), whole},
		{"original hash algorithm", owner, blob.MustParse("sha1-" + strings.Repeat("2", 40))},
		{"swapped refs", whole, owner},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := immichAssetID(tt.owner, tt.whole)
			if !immichValidUUID(got) {
				t.Fatalf("asset ID %q is not UUIDv4-shaped", got)
			}
			if again := immichAssetID(tt.owner, tt.whole); again != got {
				t.Fatalf("asset ID is not deterministic: %q then %q", got, again)
			}
			if (tt.name == "same inputs") != (got == want) {
				t.Fatalf("asset ID %q unexpectedly matches/differs from %q", got, want)
			}
		})
	}
}

func TestImmichUploadDeterministicAssetID(t *testing.T) {
	signer, pub := newSigner(t)
	const original = "identical original bytes across independent adapter state"
	whole := blob.RefFromString(original)
	owner := blob.RefFromString(pub)
	want := immichAssetID(owner, whole)
	var first *immichAsset
	var firstUser string
	for _, corpus := range []bool{false, true} {
		t.Run(fmt.Sprintf("corpus=%t", corpus), func(t *testing.T) {
			for _, renamed := range []bool{false, true} {
				t.Run(fmt.Sprintf("renamed=%t", renamed), func(t *testing.T) {
					// Independent primary stores, indexes, and random Immich owner
					// UUIDs share only the signing identity and original bytes.
					h := newImmichTestHandlerWithSigner(t, corpus, signer, pub)
					var fields map[string]string
					if renamed {
						h.email = "different-login@example.com"
						h.password = "different-password"
						h.prefix = "/different-mount/api/"
						fields = immichDedupChangedFields()
					}
					token := immichTestLogin(t, h)
					id := immichUploadedID(t, immichTestUpload(t, h, token, original, fields), 201)
					if id != want {
						t.Fatalf("asset ID = %q, want %q for owner %v and wholeRef %v", id, want, owner, whole)
					}
					a := immichDedupAsset(t, h, id)
					if first == nil {
						copy := *a
						first, firstUser = &copy, h.userID()
					} else {
						if h.userID() == firstUser {
							t.Fatal("independent test state unexpectedly reused the Immich user UUID")
						}
						if renamed && a.FileRef == first.FileRef {
							t.Fatal("changed filename/timestamps did not produce a different file schema")
						}
						if a.PermanodeRef != first.PermanodeRef {
							t.Fatal("same owner and wholeRef changed the content-planned permanode")
						}
					}
					if got := immichUploadedID(t, immichTestUpload(t, h, token, original, immichDedupChangedFields()), 200); got != want {
						t.Fatalf("duplicate asset ID = %q, want %q", got, want)
					}
					const different = "different original bytes"
					got := immichUploadedID(t, immichTestUpload(t, h, token, different, nil), 201)
					if got == want || got != immichAssetID(owner, blob.RefFromString(different)) {
						t.Fatalf("different original got incorrect asset ID %q", got)
					}
				})
			}
		})
	}
}

func TestImmichUploadKeepsExistingAssetID(t *testing.T) {
	for _, sha1Only := range []bool{false, true} {
		t.Run(fmt.Sprintf("sha1Only=%t", sha1Only), func(t *testing.T) {
			h := newImmichTestHandler(t)
			token := immichTestLogin(t, h)
			a := immichStateTestAsset(t, h, 1)
			a.ID = "11111111-1111-4111-8111-111111111111"
			if a.ID == immichAssetID(h.search.Owner(), a.WholeRef) {
				t.Fatal("legacy fixture unexpectedly has the derived ID")
			}
			b := h.metadata.BeginBatch()
			immichStateTestSetAsset(t, b, a)
			b.Set("sequence", "1")
			if err := h.metadata.CommitBatch(b); err != nil {
				t.Fatal(err)
			}
			immichDedupResetMetadata(t, h)
			if sha1Only {
				immichTestUseMemoryMetadata(t, h)
				immichDedupMakeLegacy(t, h, immichDedupAsset(t, h, a.ID))
			}
			id := immichUploadedID(t, immichTestUpload(t, h, token, "native state original 1", immichDedupChangedFields()), 200)
			if id != a.ID {
				t.Fatalf("existing ID changed to %q, want %q", id, a.ID)
			}
		})
	}
}

func TestImmichAssetIDCollisionFailsClosed(t *testing.T) {
	h := newImmichTestHandler(t)
	a := immichStateTestAsset(t, h, 1)
	other := immichStateTestAsset(t, h, 2)
	// Force the otherwise astronomically unlikely truncated-digest collision.
	other.ID = a.ID
	b := h.metadata.BeginBatch()
	immichStateTestSetAsset(t, b, a)
	b.Set("sequence", "1")
	if err := h.metadata.CommitBatch(b); err != nil {
		t.Fatal(err)
	}
	before := immichDedupPublished(t, h)
	b = h.metadata.BeginBatch()
	immichStateTestSetAsset(t, b, other)
	b.Set("sequence", strconv.FormatUint(other.Sequence, 10))
	if err := h.metadata.CommitBatch(b); err == nil {
		t.Fatal("same asset ID replaced a different original")
	}
	immichDedupAssertPublished(t, h, before)
	got := immichDedupAsset(t, h, a.ID)
	if got.WholeRef != a.WholeRef || got.FileRef != a.FileRef || got.PermanodeRef != a.PermanodeRef {
		t.Fatalf("collision replaced the original: %+v", got)
	}
	if seq, err := h.mobileSequence(); err != nil || seq != 1 {
		t.Fatalf("collision changed the published sequence: %d, %v", seq, err)
	}
}
