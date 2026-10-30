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

// Package attrclaims signs claims that change permanodes' attributes,
// and the planned permanodes that tools such as pk-put -filenodes and pk
// takeout make, for clients and importers that keep permanodes in sync
// with another source.
package attrclaims

import (
	"context"
	"fmt"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
)

// A Change is a change to an attribute of a permanode.
type Change struct {
	Op    string // "set", "add", or "del" (with Value "" for all of the attribute's values)
	Attr  string
	Value string
}

// maxClaimBytes is about the most attribute bytes to put in one claim,
// leaving room under schema.MaxSchemaBlobSize for the claims' other
// fields and JSON escaping.
const maxClaimBytes = schema.MaxSchemaBlobSize / 4

// Sign returns the signed claims, dated t, that make changes to pn. If
// the changes are too many for one claim, they're in several, each dated
// a nanosecond after the last, so they apply in order.
func Sign(ctx context.Context, signer *schema.Signer, pn blob.Ref, changes []Change, t time.Time) ([]string, error) {
	var signed []string
	for len(changes) > 0 {
		n, size := 0, 0
		for n < len(changes) {
			cs := len(changes[n].Attr) + len(changes[n].Value) + 150
			if cs > schema.MaxSchemaBlobSize/2 {
				return nil, fmt.Errorf("value of attribute %q of %v is too large for a claim (%d bytes)", changes[n].Attr, pn, len(changes[n].Value))
			}
			if n > 0 && size+cs > maxClaimBytes {
				break
			}
			size += cs
			n++
		}
		s, err := signClaim(ctx, signer, pn, changes[:n], t)
		if err != nil {
			return nil, err
		}
		signed = append(signed, s)
		changes = changes[n:]
		t = t.Add(time.Nanosecond)
	}
	return signed, nil
}

// signClaim returns one signed claim, dated t, of changes to pn.
func signClaim(ctx context.Context, signer *schema.Signer, pn blob.Ref, changes []Change, t time.Time) (string, error) {
	var claims []*schema.Builder
	for _, c := range changes {
		switch c.Op {
		case "set":
			claims = append(claims, schema.NewSetAttributeClaim(pn, c.Attr, c.Value))
		case "add":
			claims = append(claims, schema.NewAddAttributeClaim(pn, c.Attr, c.Value))
		case "del":
			claims = append(claims, schema.NewDelAttributeClaim(pn, c.Attr, c.Value))
		default:
			return "", fmt.Errorf("unknown attribute change op %q", c.Op)
		}
	}
	claim := claims[0]
	if len(claims) > 1 {
		var err error
		if claim, err = schema.NewMultiClaim(claims...); err != nil {
			return "", err
		}
		claim.SetMultiClaimPermanode(pn)
	}
	claim.SetClaimDate(t)
	signed, err := claim.SignAt(ctx, signer, t)
	if err != nil {
		return "", fmt.Errorf("signing claim: %w", err)
	}
	return signed, nil
}

// PlannedPermanode returns the planned permanode with key, signed by
// signer as pk-put -filenodes signs them (dated the Unix epoch), so that
// the same signer always makes the same one.
func PlannedPermanode(ctx context.Context, signer *schema.Signer, key string) (signed string, ref blob.Ref, err error) {
	signed, err = schema.NewPlannedPermanode(key).SignAt(ctx, signer, time.Unix(0, 0))
	if err != nil {
		return "", blob.Ref{}, fmt.Errorf("signing planned permanode: %w", err)
	}
	return signed, blob.RefFromString(signed), nil
}
