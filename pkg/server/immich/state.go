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
	"bytes"
	"context"
	"encoding/base64"

	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/types/camtypes"
)

const (
	immichStateAttr          = "immichState"
	immichAssetIDAttr        = "immichAssetId"
	immichChecksumAttr       = "immichChecksum"
	immichRecordAttr         = "immichRecord"
	immichStateKeyPrefix     = "immichStateKey:"
	immichStateTransaction   = "immichStateTransaction"
	immichStatePrevious      = "immichStatePrevious"
	immichStateAssetCount    = "immichStateAssetCount"
	immichStateUsage         = "immichStateUsage"
	immichStateTitle         = "Immich Upload State"
	immichStateMaxValueBlob  = 6*(sorted.MaxKeySize+sorted.MaxValueSize) + 128
	immichStateAccountPNSeed = "perkeep.org/immich/account/v1\x00"
)

// immichState implements the old adapter's KV API, but the KV is entirely
// disposable. Each photo carries its immutable record reference; the account
// permanode contains only individual non-derived keys, never a catalog dump.
//
// This is a SINGLE-WRITER store. In particular, two servers must not publish
// through the same signing owner. The transaction chain detects divergent
// writers, but is not a distributed compare-and-swap or an index repair tool.
// A complete native index is required. After a possibly partially successful
// publication, this instance fails closed until the storage/index are repaired
// and the handler is reopened. Never fall back to the old memory view: doing so
// could acknowledge a different UUID for an already published upload.
//
// Uploads publish only photo receipts, not an account head. Rare account
// transactions checkpoint the catalog prefix for recovery validation, but a
// wholly missing, uncheckpointed photo tail cannot be detected from the index
// alone. Neither the query result nor its maximum sequence proves completeness.
type immichState struct {
	mu sync.Mutex
	h  *Handler // storage is deliberately resolved on every operation

	view      sorted.KeyValue
	account   blob.Ref
	accountPN string
	assets    map[string]immichAsset
	byPN      map[blob.Ref]string
	byWhole   map[blob.Ref]string
	bySeq     map[uint64]string
	usage     int64
	sequence  uint64
	tx        uint64
	head      blob.Ref
	lastDate  time.Time
	migration bool
	poison    error
	closed    bool
}

var _ sorted.KeyValue = (*immichState)(nil)

func newImmichState(h *Handler) (sorted.KeyValue, error) {
	return openImmichState(h, false)
}

// newImmichStateForMigration allows a partially imported catalog to be resumed.
// The caller validates the complete legacy graph before copying bounded batches
// and writes user|id LAST. That final transaction activates ordinary validation.
// Never serve HTTP or call initIdentity before the explicit import completes.
func newImmichStateForMigration(h *Handler) (sorted.KeyValue, error) {
	return openImmichState(h, true)
}

func openImmichState(h *Handler, migration bool) (sorted.KeyValue, error) {
	if h == nil || h.storage == nil || h.signer == nil || h.search == nil || h.search.Index() == nil {
		return nil, errors.New("immich native state requires primary storage, signer and search")
	}
	ctx := context.Background()
	// Neither a changed mount URL nor changed login credentials may assign a
	// new account. Signing binds this fixed planned key to the owner's key.
	pn, err := schema.NewPlannedPermanode(immichStateAccountPNSeed).
		SignAt(ctx, h.signer, time.Unix(0, 0))
	if err != nil {
		return nil, fmt.Errorf("immich account permanode: %w", err)
	}
	var signedBy struct {
		Signer blob.Ref `json:"camliSigner"`
	}
	if err := json.Unmarshal([]byte(pn), &signedBy); err != nil || signedBy.Signer != h.search.Owner() {
		return nil, errors.New("immich signer does not match native search owner")
	}
	s := &immichState{
		h: h, view: sorted.NewMemoryKeyValue(), accountPN: pn,
		account: blob.RefFromString(pn), migration: migration,
		assets: make(map[string]immichAsset), byPN: make(map[blob.Ref]string),
		byWhole: make(map[blob.Ref]string), bySeq: make(map[uint64]string),
	}
	if err := s.rebuild(ctx); err != nil {
		s.view.Close()
		return nil, fmt.Errorf("immich native state: %w", err)
	}
	return s, nil
}

// fetch validates both the advertised size and the content hash, with an
// absolute allocation/read bound. Do not trust the index or a Fetcher's size.
func (s *immichState) fetch(ctx context.Context, ref blob.Ref, limit int) ([]byte, error) {
	if !ref.Valid() || ref.Hash() == nil {
		return nil, fmt.Errorf("invalid or unsupported reference %v", ref)
	}
	r, size, err := s.h.storage.Fetch(ctx, ref)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	if uint64(size) > uint64(limit) {
		return nil, fmt.Errorf("blob %v exceeds %d byte limit", ref, limit)
	}
	data, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit || uint64(len(data)) != uint64(size) {
		return nil, fmt.Errorf("blob %v has invalid size: advertised %d, read %d", ref, size, len(data))
	}
	hash := ref.Hash()
	hash.Write(data)
	if !ref.HashMatches(hash) {
		return nil, fmt.Errorf("blob %v has invalid content hash", ref)
	}
	return data, nil
}

func (s *immichState) claims(ctx context.Context, pn blob.Ref) ([]camtypes.Claim, error) {
	ix := s.h.search.Index()
	ix.RLock()
	claims, err := ix.AppendClaims(ctx, nil, pn, s.h.signer.KeyIDLong(), "")
	ix.RUnlock()
	if err != nil {
		return nil, err
	}
	sort.Sort(camtypes.ClaimsByDate(claims))
	return claims, nil
}

type immichStateClaim struct {
	ops     []schema.AttributeClaim
	date    time.Time
	signer  blob.Ref
	indexed map[int]bool // children verified across all rebuilt permanodes
}

// nativeAttrs replays owner claims, checking their enclosing signed blobs in
// PRIMARY storage, including historical envelopes. The index cannot replace
// those envelopes during a restore. Parsed operations must agree with the index.
// Original file contents are intentionally NOT fetched or rehashed here.
func (s *immichState) nativeAttrs(ctx context.Context, pn blob.Ref, claims []camtypes.Claim, checked map[blob.Ref]immichStateClaim) (url.Values, error) {
	data, err := s.fetch(ctx, pn, schema.MaxSchemaBlobSize)
	if err != nil {
		return nil, fmt.Errorf("missing/corrupt primary permanode %v: %w", pn, err)
	}
	b, err := schema.BlobFromReader(pn, bytes.NewReader(data))
	if err != nil || b.Type() != schema.TypePermanode {
		return nil, fmt.Errorf("invalid primary permanode %v", pn)
	}
	attrs := make(url.Values)
	seen := make(map[blob.Ref]map[int]bool)
	for _, cl := range claims {
		envelope, ok := checked[cl.BlobRef]
		if !ok {
			data, err := s.fetch(ctx, cl.BlobRef, schema.MaxSchemaBlobSize)
			if err != nil {
				return nil, fmt.Errorf("missing/corrupt primary claim %v: %w", cl.BlobRef, err)
			}
			b, err := schema.BlobFromReader(cl.BlobRef, bytes.NewReader(data))
			if err != nil {
				return nil, err
			}
			c, ok := b.AsClaim()
			date, dateErr := b.ClaimDate()
			if !ok || dateErr != nil || c.Signer() != cl.Signer || !date.Equal(cl.Date) {
				return nil, fmt.Errorf("primary claim %v disagrees with index", cl.BlobRef)
			}
			ops, err := c.AttributeClaims()
			if err != nil {
				return nil, err
			}
			envelope = immichStateClaim{
				ops: ops, date: date, signer: c.Signer(), indexed: make(map[int]bool),
			}
			checked[cl.BlobRef] = envelope
		}
		if !envelope.date.Equal(cl.Date) || envelope.signer != cl.Signer {
			return nil, fmt.Errorf("indexed claim envelope %v disagrees with primary", cl.BlobRef)
		}
		i := cl.MultiIndex
		if i == -1 && len(envelope.ops) == 1 {
			i = 0 // ordinary, non-multi attribute claim
		}
		if i < 0 || i >= len(envelope.ops) {
			return nil, fmt.Errorf("invalid indexed child of %v", cl.BlobRef)
		}
		if seen[cl.BlobRef] == nil {
			seen[cl.BlobRef] = make(map[int]bool)
		}
		if seen[cl.BlobRef][i] {
			return nil, fmt.Errorf("duplicate indexed operation of %v", cl.BlobRef)
		}
		seen[cl.BlobRef][i] = true
		op := envelope.ops[i]
		if op.Permanode != pn || string(op.Type) != cl.Type || op.Attribute != cl.Attr || op.Value != cl.Value {
			return nil, fmt.Errorf("indexed operation disagrees with primary claim %v", cl.BlobRef)
		}
		envelope.indexed[i] = true
		if cl.Date.After(s.lastDate) {
			s.lastDate = cl.Date
		}
		switch schema.ClaimType(cl.Type) {
		case schema.DelAttributeClaim:
			if cl.Value == "" {
				delete(attrs, cl.Attr)
			} else {
				var keep []string
				for _, v := range attrs[cl.Attr] {
					if v != cl.Value {
						keep = append(keep, v)
					}
				}
				attrs[cl.Attr] = keep
			}
		case schema.SetAttributeClaim:
			delete(attrs, cl.Attr)
			if cl.Value != "" {
				attrs[cl.Attr] = []string{cl.Value}
			}
		case schema.AddAttributeClaim:
			if cl.Value != "" {
				found := false
				for _, v := range attrs[cl.Attr] {
					found = found || v == cl.Value
				}
				if !found {
					attrs[cl.Attr] = append(attrs[cl.Attr], cl.Value)
				}
			}
		}
	}
	// Checking only the rows returned by the index would resurrect revoked
	// sessions if a del-attribute child were absent. Every operation in each
	// primary envelope targeting this PN must have its corresponding row.
	for ref, positions := range seen {
		for i, op := range checked[ref].ops {
			if op.Permanode == pn && !positions[i] {
				return nil, fmt.Errorf("primary claim %v is incompletely indexed; repair/reindex required", ref)
			}
		}
	}
	return attrs, nil
}

func immichStateOne(attrs url.Values, key string, required bool) (string, error) {
	v := attrs[key]
	if len(v) == 0 && !required {
		return "", nil
	}
	if len(v) != 1 || v[0] == "" {
		return "", fmt.Errorf("expected one nonempty %s attribute", key)
	}
	return v[0], nil
}

// accountHead validates a linear history rather than silently merging two
// publishers' account changes. Every transaction, including session-only ones,
// has exactly one increasing marker and a reference to its predecessor.
func (s *immichState) accountHead(claims []camtypes.Claim) (uint64, blob.Ref, error) {
	var n uint64
	var head blob.Ref
	var date time.Time
	markers := make(map[blob.Ref]bool)
	prev := make(map[blob.Ref]string)
	for _, cl := range claims {
		if cl.Attr == immichStatePrevious {
			if _, dup := prev[cl.BlobRef]; dup || cl.Type != string(schema.SetAttributeClaim) {
				return 0, blob.Ref{}, errors.New("invalid account transaction predecessor")
			}
			prev[cl.BlobRef] = cl.Value
		}
	}
	for _, cl := range claims {
		if cl.Attr != immichStateTransaction {
			continue
		}
		v, err := strconv.ParseUint(cl.Value, 10, 64)
		wantPrev := ""
		if head.Valid() {
			wantPrev = head.String()
		}
		if err != nil || n == ^uint64(0) || v != n+1 || cl.Type != string(schema.SetAttributeClaim) ||
			prev[cl.BlobRef] != wantPrev || !cl.Date.After(date) {
			return 0, blob.Ref{}, errors.New("divergent or invalid Immich account history; single writer and index repair required")
		}
		n, head, date = v, cl.BlobRef, cl.Date
		markers[head] = true
	}
	for _, cl := range claims {
		if !markers[cl.BlobRef] {
			return 0, blob.Ref{}, errors.New("account has out-of-transaction changes; single writer and index repair required")
		}
	}
	return n, head, nil
}

type immichStateValue struct {
	Version int    `json:"version"`
	Key     string `json:"key"`
	Value   string `json:"value"`
}

func immichStateDerived(key string) bool {
	return key == "usage" || strings.HasPrefix(key, "whole|") ||
		strings.HasPrefix(key, "checksum|") || strings.HasPrefix(key, "change|")
}

func immichStatePhotoAttr(attr string) bool {
	// Checksum alone is not a receipt: ingestion can publish it before the
	// final receipt claim. These three attributes are receipt-stage only.
	return attr == immichStateAttr || attr == immichAssetIDAttr || attr == immichRecordAttr
}

// candidates uses indexed metadata, not a current-attribute search: native
// deletion of a photo, receipt claim, or association must not silently rewind
// the allocator. This is one pass over INDEX metadata, never enumeration of
// primary blobs or reading original contents. Only deleted claim envelopes
// need primary reads here; ordinary claims are fetched when validating receipts.
func (s *immichState) candidates(ctx context.Context, checked map[blob.Ref]immichStateClaim) (map[blob.Ref]bool, error) {
	pns := make(map[blob.Ref]bool) // candidate PN -> native deletion status
	// Old cross-PN publications also discover candidates whose PN metadata row
	// has been lost. Missing children are checked after the complete rebuild.
	for _, envelope := range checked {
		for _, op := range envelope.ops {
			if immichStatePhotoAttr(op.Attribute) {
				pns[op.Permanode] = false
			}
		}
	}
	ix := s.h.search.Index()
	deletions, haveDeletions := ix.(interface{ IsDeleted(blob.Ref) bool })
	var deletedClaims []blob.Ref
	ix.RLock()
	deletedAccount := haveDeletions && deletions.IsDeleted(s.account)
	for pn := range pns {
		pns[pn] = haveDeletions && deletions.IsDeleted(pn)
	}
	err := ix.EnumerateBlobMeta(ctx, func(meta camtypes.BlobMeta) bool {
		switch meta.CamliType {
		case schema.TypePermanode:
			pns[meta.Ref] = haveDeletions && deletions.IsDeleted(meta.Ref)
		case schema.TypeClaim:
			if haveDeletions && deletions.IsDeleted(meta.Ref) {
				deletedClaims = append(deletedClaims, meta.Ref)
			}
		}
		return true
	})
	ix.RUnlock()
	if err != nil {
		return nil, err
	}
	if deletedAccount {
		return nil, errors.New("native Immich account deletion is unsupported; restore state and repair/reindex required")
	}
	// AppendClaims intentionally excludes deleted envelopes. Inspect just those
	// envelopes separately, otherwise a deleted uncheckpointed receipt could
	// disappear even with a perfectly healthy index.
	for _, ref := range deletedClaims {
		data, err := s.fetch(ctx, ref, schema.MaxSchemaBlobSize)
		if err != nil {
			return nil, fmt.Errorf("checking deleted primary claim %v: %w", ref, err)
		}
		b, err := schema.BlobFromReader(ref, bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		cl, ok := b.AsClaim()
		if !ok {
			return nil, fmt.Errorf("invalid deleted primary claim %v", ref)
		}
		if cl.Signer() != s.h.search.Owner() {
			continue
		}
		switch cl.ClaimType() {
		case schema.SetAttributeClaim, schema.AddAttributeClaim, schema.DelAttributeClaim, schema.MultiClaim:
			ops, err := cl.AttributeClaims()
			if err != nil {
				return nil, err
			}
			for _, op := range ops {
				if op.Permanode == s.account || immichStatePhotoAttr(op.Attribute) {
					return nil, fmt.Errorf("native Immich claim deletion %v is unsupported; restore state and repair/reindex required", ref)
				}
			}
		}
	}
	return pns, nil
}

func (s *immichState) rebuild(ctx context.Context) error {
	claims, err := s.claims(ctx, s.account)
	if err != nil {
		return err
	}
	s.tx, s.head, err = s.accountHead(claims)
	if err != nil {
		return err
	}
	checked := make(map[blob.Ref]immichStateClaim)
	var expectedCount uint64
	var expectedUsage int64
	if len(claims) == 0 {
		// A lost/empty index is not evidence of an empty native account.
		_, err := s.fetch(ctx, s.account, schema.MaxSchemaBlobSize)
		if err == nil {
			return errors.New("account permanode exists in primary but has no indexed claims; repair/reindex required")
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("checking primary account: %w", err)
		}
	} else {
		attrs, err := s.nativeAttrs(ctx, s.account, claims, checked)
		if err != nil {
			return err
		}
		if v, err := immichStateOne(attrs, "camliNodeType", true); err != nil || v != "immich:account" {
			return errors.New("invalid Immich account node type")
		}
		count, err := immichStateOne(attrs, immichStateAssetCount, true)
		if err != nil {
			return err
		}
		expectedCount, err = strconv.ParseUint(count, 10, 64)
		if err != nil {
			return errors.New("invalid native catalog asset count")
		}
		usage, err := immichStateOne(attrs, immichStateUsage, true)
		if err != nil {
			return err
		}
		expectedUsage, err = strconv.ParseInt(usage, 10, 64)
		if err != nil || expectedUsage < 0 {
			return errors.New("invalid native catalog usage")
		}
		for attr := range attrs {
			if !strings.HasPrefix(attr, immichStateKeyPrefix) {
				continue
			}
			keyBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(attr, immichStateKeyPrefix))
			key := string(keyBytes)
			if err != nil || key == "" || len(key) > sorted.MaxKeySize ||
				attr != immichStateKeyPrefix+base64.RawURLEncoding.EncodeToString(keyBytes) ||
				immichStateDerived(key) || strings.HasPrefix(key, "asset|") {
				return fmt.Errorf("invalid account key attribute %q", attr)
			}
			v, err := immichStateOne(attrs, attr, true)
			if err != nil {
				return err
			}
			ref, ok := blob.Parse(v)
			if !ok {
				return fmt.Errorf("invalid account value ref %q", v)
			}
			data, err := s.fetch(ctx, ref, immichStateMaxValueBlob)
			if err != nil {
				return fmt.Errorf("reading account key %q: %w", key, err)
			}
			var value immichStateValue
			if err := json.Unmarshal(data, &value); err != nil {
				return err
			}
			if value.Version != 1 || value.Key != key {
				return fmt.Errorf("invalid account value for %q", key)
			}
			if err := sorted.CheckSizes(key, value.Value); err != nil {
				return err
			}
			if err := s.view.Set(key, value.Value); err != nil {
				return err
			}
		}
	}
	if v, err := s.view.Get("sequence"); err == nil {
		s.sequence, err = strconv.ParseUint(v, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid sequence: %w", err)
		}
	} else if !errors.Is(err, sorted.ErrNotFound) {
		return err
	}
	accountSequence := s.sequence
	var prefixCount uint64
	var prefixUsage int64
	pns, err := s.candidates(ctx, checked)
	if err != nil {
		return err
	}
	for pn, deleted := range pns {
		if pn == s.account {
			continue // the account history was validated separately
		}
		cl, err := s.claims(ctx, pn)
		if err != nil {
			return err
		}
		relevant := false
		for _, claim := range cl {
			relevant = relevant || immichStatePhotoAttr(claim.Attr)
		}
		if !relevant {
			continue
		}
		if deleted {
			return errors.New("native Immich photo deletion is unsupported; restore state and repair/reindex required")
		}
		attrs, err := s.nativeAttrs(ctx, pn, cl, checked)
		if err != nil {
			return err
		}
		state, err := immichStateOne(attrs, immichStateAttr, true)
		if err != nil || state != s.account.String() {
			return errors.New("invalid photo account association")
		}
		id, err := immichStateOne(attrs, immichAssetIDAttr, true)
		if err != nil {
			return err
		}
		checksum, err := immichStateOne(attrs, immichChecksumAttr, true)
		if err != nil {
			return err
		}
		v, err := immichStateOne(attrs, immichRecordAttr, true)
		if err != nil {
			return err
		}
		ref, ok := blob.Parse(v)
		if !ok {
			return fmt.Errorf("invalid photo record ref %q", v)
		}
		data, err := s.fetch(ctx, ref, sorted.MaxValueSize)
		if err != nil {
			return fmt.Errorf("reading asset %q: %w", id, err)
		}
		a, err := immichStateDecodeAsset("asset|"+id, string(data))
		if err != nil {
			return err
		}
		if a.PermanodeRef != pn || a.Checksum != checksum {
			return fmt.Errorf("asset %q disagrees with native attributes", id)
		}
		if err := s.checkAsset(a, nil); err != nil {
			return err
		}
		if a.Size > (1<<63-1)-s.usage {
			return errors.New("overflowing native catalog usage")
		}
		s.addAsset(a)
		if a.Sequence > s.sequence {
			s.sequence = a.Sequence
		}
		if a.Sequence <= accountSequence {
			prefixCount++
			prefixUsage += a.Size // bounded by the checked total usage
		}
		if err := s.view.Set("asset|"+id, string(data)); err != nil {
			return err
		}
	}
	if len(s.assets) != 0 && s.tx == 0 {
		return errors.New("photo records exist without their native account")
	}
	// Check cross-PN children too. In particular, an old account transaction
	// can reveal a photo whose entire set of query/index rows has disappeared.
	// This is linear in the envelopes already fetched, not a per-photo scan of
	// every other photo's history.
	for ref, envelope := range checked {
		for i, op := range envelope.ops {
			if (op.Permanode == s.account || s.byPN[op.Permanode] != "" ||
				immichStatePhotoAttr(op.Attribute)) && !envelope.indexed[i] {
				return fmt.Errorf("primary claim %v is incompletely indexed; repair/reindex required", ref)
			}
		}
	}
	// An activated account checkpoints an EXACT prefix. Newer photos must not
	// hide a missing older receipt by merely meeting the total count/usage.
	// During explicit migration, however, a bounded batch can reserve the final
	// legacy sequence before all its earlier photos have been imported. The
	// validated source checks this partial view; UID-last refreshes the exact
	// snapshot and restores ordinary validation.
	_, uidErr := s.view.Get("user|id")
	incompleteMigration := s.migration && errors.Is(uidErr, sorted.ErrNotFound)
	if uidErr != nil && !errors.Is(uidErr, sorted.ErrNotFound) {
		return uidErr
	}
	if prefixCount < expectedCount || prefixUsage < expectedUsage ||
		(!incompleteMigration && (prefixCount != expectedCount || prefixUsage != expectedUsage)) {
		return errors.New("native query catalog prefix disagrees with durable asset count/usage; repair/reindex required")
	}
	// ACKs never supply authority for the sequence allocator. In particular a
	// partial metadata cursor at N and SyncComplete|N both require sequence N,
	// not just the last fully consumed asset. Missing catalog data must not be
	// concealed by promoting the sequence to an ACK's position.
	it := s.view.Find(immichAckPrefix, immichAckPrefix+"\xff")
	for it.Next() {
		fullKey := it.Key()
		key := strings.TrimPrefix(fullKey, immichAckPrefix)
		// HTTP session keys themselves contain a separator ("session|hash").
		// The entity is the LAST component; short fixture/legacy keys also work.
		separator := strings.LastIndex(key, "|")
		cursor, err := immichMobileParseAck(it.Value())
		if separator <= 0 || err != nil || cursor.Type != key[separator+1:] || cursor.Sequence > s.sequence {
			it.Close()
			return fmt.Errorf("invalid native acknowledgement %q or cursor ahead of catalog; repair/reindex required", fullKey)
		}
	}
	if err := it.Close(); err != nil {
		return err
	}
	if err := s.validateActivation(s.migration); err != nil {
		return err
	}
	if err := s.rebuildDerived(); err != nil {
		return err
	}
	return nil
}

func immichStateDecodeAsset(key, value string) (immichAsset, error) {
	var a immichAsset
	if err := sorted.CheckSizes(key, value); err != nil {
		return a, err
	}
	if err := json.Unmarshal([]byte(value), &a); err != nil {
		return a, fmt.Errorf("invalid asset record: %w", err)
	}
	if err := validateImmichLegacyAsset(&a); err != nil {
		return a, fmt.Errorf("invalid asset record %q: %w", key, err)
	}
	if key != "asset|"+a.ID || !a.WholeRef.Valid() {
		return a, fmt.Errorf("invalid asset identity/record %q", key)
	}
	return a, nil
}

func (s *immichState) checkAsset(a immichAsset, pending map[string]immichAsset) error {
	if a.PermanodeRef == s.account {
		return errors.New("account permanode cannot also be a photo")
	}
	if old, ok := s.assets[a.ID]; ok {
		if a.PermanodeRef != old.PermanodeRef || a.FileRef != old.FileRef ||
			a.Checksum != old.Checksum || a.Sequence != old.Sequence || a.Size != old.Size ||
			a.Type != old.Type ||
			(old.WholeRef.Valid() && a.WholeRef != old.WholeRef) {
			return fmt.Errorf("cannot replace original identity of asset %q", a.ID)
		}
	}
	if id := s.byPN[a.PermanodeRef]; id != "" && id != a.ID {
		return fmt.Errorf("permanode already belongs to asset %q", id)
	}
	if id := s.byWhole[a.WholeRef]; a.WholeRef.Valid() && id != "" && id != a.ID {
		return fmt.Errorf("whole-file digest already belongs to asset %q", id)
	}
	if id := s.bySeq[a.Sequence]; id != "" && id != a.ID {
		return fmt.Errorf("sequence already belongs to asset %q", id)
	}
	for id, other := range pending {
		if id != a.ID && (a.PermanodeRef == other.PermanodeRef || a.Sequence == other.Sequence ||
			(a.WholeRef.Valid() && a.WholeRef == other.WholeRef)) {
			return errors.New("duplicate native asset identity in transaction")
		}
	}
	return nil
}

func (s *immichState) addAsset(a immichAsset) {
	if _, exists := s.assets[a.ID]; !exists {
		s.usage += a.Size
	}
	s.assets[a.ID], s.byPN[a.PermanodeRef], s.bySeq[a.Sequence] = a, a.ID, a.ID
	if a.WholeRef.Valid() {
		s.byWhole[a.WholeRef] = a.ID
	}
}

func (s *immichState) validateActivation(allowIncomplete bool) error {
	id, err := s.view.Get("user|id")
	if err != nil && !errors.Is(err, sorted.ErrNotFound) {
		return err
	}
	if errors.Is(err, sorted.ErrNotFound) {
		if !allowIncomplete && s.tx != 0 {
			return errors.New("incomplete native import/account has no user|id; resume explicit legacy migration")
		}
		return nil
	}
	if !immichValidUUID(id) {
		return errors.New("invalid native user identity")
	}
	for _, a := range s.assets {
		if a.LivePhotoVideoID != "" {
			video, ok := s.assets[a.LivePhotoVideoID]
			if !ok || video.Type != "VIDEO" || video.Sequence >= a.Sequence {
				return fmt.Errorf("asset %q refers to invalid Live Photo video", a.ID)
			}
		}
	}
	s.migration = false
	return nil
}

func (s *immichState) rebuildDerived() error {
	b := s.view.BeginBatch()
	// Stable iteration order also chooses a deterministic compatibility
	// checksum hint in the event of a SHA-1 collision. WholeRef is the identity.
	assets := make([]immichAsset, 0, len(s.assets))
	for _, a := range s.assets {
		assets = append(assets, a)
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].Sequence < assets[j].Sequence })
	for _, a := range assets {
		if a.WholeRef.Valid() {
			b.Set("whole|"+a.WholeRef.String(), a.ID)
		}
		b.Set("checksum|"+a.Checksum, a.ID)
		b.Set(fmt.Sprintf("change|%020d", a.Sequence), a.ID)
	}
	if len(assets) != 0 {
		b.Set("usage", strconv.FormatInt(s.usage, 10))
	}
	if s.sequence != 0 {
		b.Set("sequence", strconv.FormatUint(s.sequence, 10))
	}
	return s.view.CommitBatch(b)
}

func (s *immichState) ready() error {
	if s.closed {
		return errors.New("immich native state is closed")
	}
	return s.poison
}

func (s *immichState) Get(key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return "", err
	}
	return s.view.Get(key)
}

type immichStateErrorIterator struct{ err error }

func (i *immichStateErrorIterator) Next() bool         { return false }
func (i *immichStateErrorIterator) Key() string        { return "" }
func (i *immichStateErrorIterator) Value() string      { return "" }
func (i *immichStateErrorIterator) KeyBytes() []byte   { return nil }
func (i *immichStateErrorIterator) ValueBytes() []byte { return nil }
func (i *immichStateErrorIterator) Close() error       { return i.err }

func (s *immichState) Find(start, end string) sorted.Iterator {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return &immichStateErrorIterator{err}
	}
	return s.view.Find(start, end)
}

func (s *immichState) BeginBatch() sorted.BatchMutation { return sorted.NewBatchMutation() }

func (s *immichState) Set(key, value string) error {
	b := s.BeginBatch()
	b.Set(key, value)
	return s.CommitBatch(b)
}

func (s *immichState) Delete(key string) error {
	b := s.BeginBatch()
	b.Delete(key)
	return s.CommitBatch(b)
}

func (s *immichState) failPublication(err error) error {
	s.poison = fmt.Errorf("immich native publication failed; repair primary/index and reopen before retrying: %w", err)
	return s.poison
}

func (s *immichState) put(ctx context.Context, text string) (blob.Ref, error) {
	ref := blob.RefFromString(text)
	sb, err := blobserver.Receive(ctx, s.h.storage, ref, strings.NewReader(text))
	if err != nil {
		return ref, err
	}
	if sb.Ref != ref || uint64(sb.Size) != uint64(len(text)) {
		return ref, errors.New("storage returned an incorrect publication receipt")
	}
	// A replica may report/index success without actually storing the primary
	// blob. Checking the configured read side is essential before updating KV.
	_, err = s.fetch(ctx, ref, len(text))
	return ref, err
}

func (s *immichState) CommitBatch(batch sorted.BatchMutation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return err
	}
	m, ok := batch.(interface{ Mutations() []sorted.Mutation })
	if !ok {
		return errors.New("invalid Immich batch")
	}
	// Last mutation of a key wins. Validate BEFORE any native writes.
	changes := make(map[string]sorted.Mutation)
	for _, mutation := range m.Mutations() {
		if err := sorted.CheckSizes(mutation.Key(), mutation.Value()); err != nil {
			return err
		}
		if mutation.Key() == "" {
			return errors.New("empty native state key")
		}
		if !immichStateDerived(mutation.Key()) {
			changes[mutation.Key()] = mutation
		}
	}
	// Mobile ACK handlers can resend all previously acknowledged positions,
	// including on an otherwise empty sync. Do not retain a signed transaction
	// forever for a KV operation that has no effect.
	for key, mutation := range changes {
		current, err := s.view.Get(key)
		if err != nil && !errors.Is(err, sorted.ErrNotFound) {
			return err
		}
		if (mutation.IsDelete() && errors.Is(err, sorted.ErrNotFound)) ||
			(!mutation.IsDelete() && err == nil && current == mutation.Value()) {
			delete(changes, key)
		}
	}
	if len(changes) == 0 {
		return nil
	}
	ctx := context.Background()
	// A complete index and one writer are prerequisites, not a distributed
	// concurrency protocol. Confirm each photo's exact four children with
	// point reads, not an account marker that no longer exists on uploads.
	// Never scan/sort the growing account history on the upload path.
	ix := s.h.search.Index()
	checker, fast := ix.(interface {
		HasClaim(context.Context, camtypes.Claim) (bool, error)
	})
	pending := make(map[string]immichAsset)
	nextSequence := s.sequence
	nextUsage := s.usage
	nextCount := len(s.assets)
	for key, mutation := range changes {
		if key == "user|id" {
			if mutation.IsDelete() || !immichValidUUID(mutation.Value()) {
				return errors.New("cannot delete or invalidate account identity")
			}
			if id, err := s.view.Get(key); err == nil && id != mutation.Value() {
				return errors.New("cannot replace account identity")
			}
		}
		if key == "sequence" {
			if mutation.IsDelete() {
				return errors.New("cannot delete account sequence")
			}
			n, err := strconv.ParseUint(mutation.Value(), 10, 64)
			if err != nil || n < s.sequence {
				return errors.New("invalid or decreasing account sequence")
			}
			nextSequence = n
		}
		if !strings.HasPrefix(key, "asset|") {
			continue
		}
		if mutation.IsDelete() {
			return errors.New("native asset identities cannot be deleted")
		}
		a, err := immichStateDecodeAsset(key, mutation.Value())
		if err != nil {
			return err
		}
		if err := s.checkAsset(a, pending); err != nil {
			return err
		}
		if _, exists := s.assets[a.ID]; !exists {
			if !s.migration && a.Sequence <= s.sequence {
				return errors.New("new asset must advance the account sequence")
			}
			if a.Size > (1<<63-1)-nextUsage {
				return errors.New("overflowing account usage")
			}
			nextUsage += a.Size
			nextCount++
		}
		pending[a.ID] = a
	}
	receiptSequence := s.sequence
	for _, a := range pending {
		if a.Sequence > receiptSequence {
			receiptSequence = a.Sequence
		}
		if a.Sequence > nextSequence {
			nextSequence = a.Sequence
		}
	}
	// Sequence beside a new receipt is an allocator result, not account state.
	// An explicit bump BEYOND all receipts is a genuine durable gap, however;
	// preserve it even if the same batch happens to contain a photo.
	accountWrite := s.tx == 0 || nextSequence > receiptSequence
	for key := range changes {
		if key != "sequence" && !strings.HasPrefix(key, "asset|") {
			accountWrite = true
		}
	}
	if len(pending) == 0 {
		accountWrite = true
	}
	if accountWrite {
		if s.tx == ^uint64(0) {
			return errors.New("native transaction sequence exhausted")
		}
		// Older index implementations have no point confirmation. Their rare
		// account writes retain the conservative history check, never uploads.
		if !fast {
			cl, err := s.claims(ctx, s.account)
			if err != nil {
				return s.failPublication(err)
			}
			tx, head, err := s.accountHead(cl)
			if err != nil || tx != s.tx || head != s.head {
				return s.failPublication(errors.New("native account changed outside this single writer"))
			}
		}
	}
	// A migrated record retains its exact sequence. Preference bumps are
	// retained by the independent account sequence, not inferred from photos.
	memory := s.view.BeginBatch()
	if nextSequence != s.sequence {
		memory.Set("sequence", strconv.FormatUint(nextSequence, 10))
	}
	activating := false
	if uid, ok := changes["user|id"]; ok && !uid.IsDelete() {
		activating = true
	}
	if !s.migration || activating {
		for _, a := range pending {
			if a.LivePhotoVideoID != "" {
				video, exists := pending[a.LivePhotoVideoID]
				if !exists {
					video, exists = s.assets[a.LivePhotoVideoID]
				}
				if !exists || video.Type != "VIDEO" || video.Sequence >= a.Sequence {
					return errors.New("asset refers to invalid Live Photo video")
				}
			}
		}
		if activating {
			for _, a := range s.assets {
				if a.LivePhotoVideoID != "" {
					video, exists := pending[a.LivePhotoVideoID]
					if !exists {
						video, exists = s.assets[a.LivePhotoVideoID]
					}
					if !exists || video.Type != "VIDEO" || video.Sequence >= a.Sequence {
						return errors.New("cannot activate import with invalid Live Photo video")
					}
				}
			}
		}
	}
	if !s.migration && s.tx == 0 && !activating {
		return errors.New("initialize user|id before publishing native state")
	}
	var ops []*schema.Builder
	var confirmations []camtypes.Claim
	if accountWrite {
		// Rare account writes retain the compatible linear transaction chain.
		// A mixed account/photo batch is still one atomic cross-PN claim.
		ops = append(ops, schema.NewSetAttributeClaim(s.account, immichStateTransaction, strconv.FormatUint(s.tx+1, 10)))
		confirmations = append(confirmations, camtypes.Claim{
			Permanode: s.account, MultiIndex: 0, Type: string(schema.SetAttributeClaim),
			Attr: immichStateTransaction, Value: strconv.FormatUint(s.tx+1, 10),
		})
	}
	if accountWrite && s.tx == 0 {
		ops = append(ops,
			schema.NewSetAttributeClaim(s.account, "camliNodeType", "immich:account"),
			schema.NewSetAttributeClaim(s.account, "title", immichStateTitle))
	}
	if accountWrite {
		// Always refresh the prefix, including session/ACK transactions after
		// photo-only uploads whose sequence already advanced the RAM view.
		ops = append(ops,
			schema.NewSetAttributeClaim(s.account, immichStateAssetCount, strconv.Itoa(nextCount)),
			schema.NewSetAttributeClaim(s.account, immichStateUsage, strconv.FormatInt(nextUsage, 10)))
	}
	if accountWrite && s.head.Valid() {
		ops = append(ops, schema.NewSetAttributeClaim(s.account, immichStatePrevious, s.head.String()))
	}
	// Stable ordering makes histories reproducible and keeps verification easy.
	keys := make([]string, 0, len(changes)+1)
	for key := range changes {
		if key == "sequence" && !accountWrite {
			continue
		}
		keys = append(keys, key)
	}
	if accountWrite && nextSequence != 0 {
		if _, set := changes["sequence"]; !set {
			keys = append(keys, "sequence")
		}
	}
	sort.Strings(keys)
	type preparedBlob struct{ text string }
	var blobs []preparedBlob
	checked := make(map[blob.Ref]immichStateClaim)
	checksums := make(map[string]immichAsset)
	for _, key := range keys {
		mutation, explicit := changes[key]
		value, deleted := "", false
		if explicit {
			value, deleted = mutation.Value(), mutation.IsDelete()
		}
		if key == "sequence" {
			value = strconv.FormatUint(nextSequence, 10)
		}
		if deleted {
			memory.Delete(key)
		} else {
			memory.Set(key, value)
		}
		if strings.HasPrefix(key, "asset|") {
			a := pending[strings.TrimPrefix(key, "asset|")]
			// Do not overwrite another API account's or writer's receipt when
			// attaching to an existing native photo.
			photoClaims, err := s.claims(ctx, a.PermanodeRef)
			if err != nil {
				return err
			}
			attrs, err := s.nativeAttrs(ctx, a.PermanodeRef, photoClaims, checked)
			if err != nil {
				return err
			}
			if state, err := immichStateOne(attrs, immichStateAttr, false); err != nil || (state != "" && state != s.account.String()) {
				return errors.New("photo already belongs to a different Immich account")
			}
			if id, err := immichStateOne(attrs, immichAssetIDAttr, false); err != nil || (id != "" && id != a.ID) {
				return errors.New("photo already has a different Immich asset identity")
			}
			ref := blob.RefFromString(value)
			blobs = append(blobs, preparedBlob{value})
			for _, attr := range [][2]string{
				{immichStateAttr, s.account.String()}, {immichAssetIDAttr, a.ID},
				{immichChecksumAttr, a.Checksum}, {immichRecordAttr, ref.String()},
			} {
				confirmations = append(confirmations, camtypes.Claim{
					Permanode: a.PermanodeRef, MultiIndex: len(ops),
					Type: string(schema.SetAttributeClaim), Attr: attr[0], Value: attr[1],
				})
				ops = append(ops, schema.NewSetAttributeClaim(a.PermanodeRef, attr[0], attr[1]))
			}
			if a.WholeRef.Valid() {
				memory.Set("whole|"+a.WholeRef.String(), a.ID)
			}
			// Pick the same SHA-1 compatibility hint as a fresh rebuild.
			if previous, ok := checksums[a.Checksum]; !ok || previous.Sequence < a.Sequence {
				checksums[a.Checksum] = a
			}
			memory.Set(fmt.Sprintf("change|%020d", a.Sequence), a.ID)
			continue
		}
		attr := immichStateKeyPrefix + base64.RawURLEncoding.EncodeToString([]byte(key))
		if deleted {
			ops = append(ops, schema.NewDelAttributeClaim(s.account, attr, ""))
		} else {
			data, err := json.Marshal(immichStateValue{Version: 1, Key: key, Value: value})
			if err != nil {
				return err
			}
			blobs = append(blobs, preparedBlob{string(data)})
			ops = append(ops, schema.NewSetAttributeClaim(s.account, attr, blob.RefFromBytes(data).String()))
		}
	}
	if len(pending) != 0 {
		memory.Set("usage", strconv.FormatInt(nextUsage, 10))
	}
	for checksum, a := range checksums {
		if id, err := s.view.Get("checksum|" + checksum); err != nil || s.assets[id].Sequence <= a.Sequence {
			memory.Set("checksum|"+checksum, a.ID)
		}
	}
	// Claim dates must increase even across restarts and clock rollback.
	at := time.Now().UTC()
	if !at.After(s.lastDate) {
		at = s.lastDate.Add(time.Second)
	}
	claim, err := schema.NewMultiClaim(ops...)
	if err != nil {
		return err
	}
	// Builder.SignAt calls Blob(), which panics on an oversized schema.
	// Serialize and bound the unsigned claim BEFORE signing; the signature
	// must fit too, and neither check may publish any blobs.
	unsigned, err := claim.SetClaimDate(at).SetSigner(s.h.search.Owner()).JSON()
	if err != nil {
		return err
	}
	if len(unsigned) > schema.MaxSchemaBlobSize {
		return fmt.Errorf("Immich transaction exceeds native schema limit of %d bytes; use smaller batches", schema.MaxSchemaBlobSize)
	}
	text, err := s.h.signer.SignJSON(ctx, unsigned, at)
	if err != nil {
		return err
	}
	if len(text) > schema.MaxSchemaBlobSize {
		return fmt.Errorf("Immich transaction exceeds native schema limit of %d bytes; use smaller batches", schema.MaxSchemaBlobSize)
	}
	// Immutable payloads and the account PN precede the ONE atomic publication.
	// No local rows become visible until the primary and index both confirm it.
	for _, b := range blobs {
		if _, err := s.put(ctx, b.text); err != nil {
			return err // no claim was attempted, so retrying is unambiguous
		}
	}
	if s.tx == 0 {
		if _, err := s.put(ctx, s.accountPN); err != nil {
			return s.failPublication(err)
		}
	}
	ref, err := s.put(ctx, text)
	if err != nil {
		return s.failPublication(err)
	}
	for i := range confirmations {
		confirmations[i].BlobRef = ref
		confirmations[i].Signer = s.h.search.Owner()
		confirmations[i].Date = at
	}
	if fast {
		ix.RLock()
		var confirmErr error
		for _, cl := range confirmations {
			ok, err := checker.HasClaim(ctx, cl)
			if err != nil {
				confirmErr = err
				break
			}
			if !ok {
				confirmErr = errors.New("native index did not confirm every published photo/transaction operation")
				break
			}
		}
		ix.RUnlock()
		if confirmErr != nil {
			return s.failPublication(confirmErr)
		}
	} else {
		// This fallback scans only the touched photos (and the account on a
		// genuine account write), never all catalog/account history per upload.
		published := make(map[blob.Ref][]camtypes.Claim)
		for _, want := range confirmations {
			rows, ok := published[want.Permanode]
			if !ok {
				var err error
				rows, err = s.claims(ctx, want.Permanode)
				if err != nil {
					return s.failPublication(err)
				}
				published[want.Permanode] = rows
			}
			found := false
			for _, got := range rows {
				if got.BlobRef == want.BlobRef && got.Signer == want.Signer &&
					got.Permanode == want.Permanode && got.MultiIndex == want.MultiIndex &&
					got.Date.Equal(want.Date) && got.Type == want.Type && got.Attr == want.Attr && got.Value == want.Value {
					found = true
					break
				}
			}
			if !found {
				return s.failPublication(errors.New("native index did not confirm every published photo/transaction operation"))
			}
		}
		if accountWrite {
			n, head, err := s.accountHead(published[s.account])
			if err != nil || n != s.tx+1 || head != ref {
				return s.failPublication(errors.New("native index did not confirm the published transaction"))
			}
		}
	}
	if err := s.view.CommitBatch(memory); err != nil {
		return s.failPublication(err)
	}
	for _, a := range pending {
		s.addAsset(a)
	}
	s.sequence, s.usage, s.lastDate = nextSequence, nextUsage, at
	if accountWrite {
		s.tx, s.head = s.tx+1, ref
	}
	if activating {
		s.migration = false
	}
	return nil
}

func (s *immichState) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return s.view.Close()
}
