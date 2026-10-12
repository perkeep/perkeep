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

package index

import (
	"context"
	"errors"
	"fmt"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/types/camtypes"
)

// HasClaim reports whether the exact, non-deleted attribute operation cl is
// present in the underlying index, even when an in-memory corpus is enabled.
// The caller must hold x.RLock.
//
// Lookup requires the indexed date's spelling to match
// schema.RFC3339FromTime(cl.Date). A time.Time cannot recover alternative
// original spellings (such as an explicit timezone offset or trailing fractional
// zeroes); claims written with the canonical schema date format are supported.
//
// MultiIndex -1 selects an ordinary claim. Zero tries multi-claim child zero
// and then an ordinary claim, because parsed ordinary claims also have index
// zero. Positive indices select only that multi-claim child.
func (x *Index) HasClaim(ctx context.Context, cl camtypes.Claim) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !cl.BlobRef.Valid() || !cl.Signer.Valid() || !cl.Permanode.Valid() ||
		cl.Target.Valid() || cl.MultiIndex < -1 {
		return false, nil
	}
	keyID, err := x.KeyId(ctx, cl.Signer)
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if errors.Is(err, sorted.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	base := keyPermanodeClaim.Key(cl.Permanode, keyID, schema.RFC3339FromTime(cl.Date), cl.BlobRef)
	keys := []string{base}
	if cl.MultiIndex >= 0 {
		keys[0] = fmt.Sprintf("%s|%020d", base, cl.MultiIndex)
		if cl.MultiIndex == 0 {
			keys = append(keys, base)
		}
	}
	for _, key := range keys {
		value, err := x.s.Get(key)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return false, ctxErr
		}
		if errors.Is(err, sorted.ErrNotFound) {
			continue
		}
		if err != nil {
			return false, err
		}
		got, ok := kvClaim(key, value, blob.Parse)
		index := cl.MultiIndex
		if index == -1 {
			index = 0
		}
		if !ok || got.BlobRef != cl.BlobRef || got.Signer != cl.Signer ||
			got.Permanode != cl.Permanode || !got.Date.Equal(cl.Date) ||
			got.Type != cl.Type || got.Attr != cl.Attr || got.Value != cl.Value ||
			got.MultiIndex != index || got.Target != cl.Target {
			continue
		}
		deleted := x.IsDeleted(cl.BlobRef)
		if err := ctx.Err(); err != nil {
			return false, err
		}
		return !deleted, nil
	}
	return false, nil
}
