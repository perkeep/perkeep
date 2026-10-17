/*
Copyright 2026 The Perkeep Authors

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

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/sorted"
)

// existingAsset finds an upload receipt by its Perkeep whole-file digest.
// SHA-1-only receipts from older metadata stores are verified against their
// original bytes before they are migrated. The caller must hold h.mu.
func (h *ImmichHandler) existingAsset(ctx context.Context, wholeRef blob.Ref, checksum string) (*immichAsset, error) {
	if !wholeRef.Valid() {
		return nil, errors.New("immich: invalid whole-file digest")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	wholeKey := "whole|" + wholeRef.String()
	id, err := h.metadata.Get(wholeKey)
	if err == nil {
		a, err := h.asset(id)
		if err != nil {
			return nil, fmt.Errorf("immich: loading whole-file receipt %q: %w", id, err)
		}
		if a.ID != id || a.WholeRef != wholeRef {
			return nil, fmt.Errorf("immich: corrupt whole-file receipt %q", id)
		}
		return a, nil
	}
	if !errors.Is(err, sorted.ErrNotFound) {
		return nil, fmt.Errorf("immich: finding whole-file receipt: %w", err)
	}

	id, err = h.metadata.Get("checksum|" + checksum)
	if errors.Is(err, sorted.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("immich: finding legacy checksum receipt: %w", err)
	}
	a, err := h.asset(id)
	if err != nil {
		return nil, fmt.Errorf("immich: loading legacy checksum receipt %q: %w", id, err)
	}
	if a.ID != id || a.Checksum != checksum {
		return nil, fmt.Errorf("immich: corrupt legacy checksum receipt %q", id)
	}
	if !a.WholeRef.Valid() {
		fr, err := schema.NewFileReader(ctx, h.storage, a.FileRef)
		if err != nil {
			return nil, fmt.Errorf("immich: opening legacy original: %w", err)
		}
		hash := blob.NewHash()
		n, err := io.Copy(hash, fr)
		closeErr := fr.Close()
		if err != nil {
			return nil, fmt.Errorf("immich: hashing legacy original: %w", err)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("immich: closing legacy original: %w", closeErr)
		}
		if n != fr.Size() {
			return nil, fmt.Errorf("immich: legacy original size is %d; read %d bytes", fr.Size(), n)
		}
		a.WholeRef = blob.RefFromHash(hash)
	}
	if a.WholeRef != wholeRef {
		// Immich's SHA-1 index is only a compatibility hint. A collision
		// must not acknowledge an unrelated original as already backed up.
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(a)
	if err != nil {
		return nil, fmt.Errorf("immich: encoding migrated receipt: %w", err)
	}
	assetKey := "asset|" + a.ID
	// Some sorted backends silently drop oversized batch mutations. Validate
	// both writes before opening a batch, which may hold a transaction.
	if err := sorted.CheckSizes(assetKey, string(data)); err != nil {
		return nil, fmt.Errorf("immich: migrated receipt size: %w", err)
	}
	if err := sorted.CheckSizes(wholeKey, a.ID); err != nil {
		return nil, fmt.Errorf("immich: migrated whole-file mapping size: %w", err)
	}
	b := h.metadata.BeginBatch()
	b.Set(assetKey, string(data))
	b.Set(wholeKey, a.ID)
	if err := h.metadata.CommitBatch(b); err != nil {
		return nil, fmt.Errorf("immich: migrating whole-file receipt: %w", err)
	}
	return a, nil
}

// findPermanode finds an owner's permanode whose current camliContent refers to
// a file with the given whole-file digest. A zero ref and nil error mean that
// the index has no matching permanode, not that the index is fully caught up.
//
// The caller must serialize this lookup with publication and receipt storage.
// This method does not modify an existing permanode or its attributes.
func (h *ImmichHandler) findPermanode(ctx context.Context, wholeRef blob.Ref) (blob.Ref, error) {
	if !wholeRef.Valid() {
		return blob.Ref{}, errors.New("immich: invalid whole-file digest")
	}
	if h.search == nil || h.search.Index() == nil {
		return blob.Ref{}, errors.New("immich: duplicate detection requires a search index")
	}
	if err := ctx.Err(); err != nil {
		return blob.Ref{}, err
	}

	ix := h.search.Index()
	ix.RLock()
	files, err := ix.ExistingFileSchemas(wholeRef)
	ix.RUnlock()
	if err != nil {
		return blob.Ref{}, fmt.Errorf("immich: finding file schemas for %v: %w", wholeRef, err)
	}

	// FileConstraint.WholeRef requires an in-memory corpus and otherwise
	// silently fails to match. Resolve all file schemas through the index
	// instead: identical bytes can have different filenames and timestamps.
	for _, fileRef := range files[wholeRef.String()] {
		if err := ctx.Err(); err != nil {
			return blob.Ref{}, err
		}
		// Query takes the index read lock itself. Do not retain the lock
		// from ExistingFileSchemas across this call.
		res, err := h.search.Query(ctx, &search.SearchQuery{
			Constraint: &search.Constraint{
				Permanode: &search.PermanodeConstraint{
					Attr:  "camliContent",
					Value: fileRef.String(),
				},
			},
			Limit: 1,
			// The default permanode sort (CreatedDesc) requires a corpus.
			Sort: search.Unsorted,
		})
		if err != nil {
			return blob.Ref{}, fmt.Errorf("immich: finding permanode for file %v: %w", fileRef, err)
		}
		if err := ctx.Err(); err != nil {
			return blob.Ref{}, err
		}
		if len(res.Blobs) != 0 {
			pn := res.Blobs[0].Blob
			if err := h.verifyPermanode(ctx, pn, fileRef, wholeRef); err != nil {
				return blob.Ref{}, err
			}
			return pn, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return blob.Ref{}, err
	}
	return blob.Ref{}, nil
}

// verifyPermanode checks the storage read side, not just the index. Replicated
// publication can index a claim while its primary blob write fails. Reusing
// that index entry must not turn a failed upload into a successful receipt.
// Missing native data requires repair or reindexing; never overwrite the
// existing object or create another permanode to work around the failure.
func (h *ImmichHandler) verifyPermanode(ctx context.Context, pn, fileRef, wholeRef blob.Ref) error {
	ix := h.search.Index()
	ix.RLock()
	claims, err := ix.AppendClaims(ctx, nil, pn, h.signer.KeyIDLong(), "camliContent")
	ix.RUnlock()
	if err != nil {
		return fmt.Errorf("immich: checking native content claims for %v: %w", pn, err)
	}
	if len(claims) == 0 {
		return fmt.Errorf("immich: native permanode %v has no owner content claims", pn)
	}
	// Conservatively require every live owner camliContent claim, including
	// historical operations: the index cannot recover their signed envelopes.
	missing := map[blob.Ref]bool{pn: true}
	for _, claim := range claims {
		missing[claim.BlobRef] = true
	}
	refs := make([]blob.Ref, 0, len(missing))
	for ref := range missing {
		refs = append(refs, ref)
	}
	if err := h.storage.StatBlobs(ctx, refs, func(sb blob.SizedRef) error {
		delete(missing, sb.Ref)
		return nil
	}); err != nil {
		return fmt.Errorf("immich: checking stored native permanode %v: %w", pn, err)
	}
	for ref := range missing {
		return fmt.Errorf("immich: native permanode %v is missing stored blob %v", pn, ref)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// ExistingFileSchemas is only an index hint: its schemas and their chunks
	// may no longer exist. Read the entire candidate from primary storage and
	// verify its actual bytes before accepting the native content identity.
	fr, err := schema.NewFileReader(ctx, h.storage, fileRef)
	if err != nil {
		return fmt.Errorf("immich: opening native original %v: %w", fileRef, err)
	}
	hash := blob.NewHash()
	n, err := io.Copy(hash, fr)
	closeErr := fr.Close()
	if err != nil {
		return fmt.Errorf("immich: hashing native original %v: %w", fileRef, err)
	}
	if closeErr != nil {
		return fmt.Errorf("immich: closing native original %v: %w", fileRef, closeErr)
	}
	if n != fr.Size() {
		return fmt.Errorf("immich: native original %v has size %d; read %d bytes", fileRef, fr.Size(), n)
	}
	if got := blob.RefFromHash(hash); got != wholeRef {
		return fmt.Errorf("immich: native original %v hashes to %v, not %v", fileRef, got, wholeRef)
	}
	return ctx.Err()
}
