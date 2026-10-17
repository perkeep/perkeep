/*
Copyright 2026 The Perkeep Authors.

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

package client

import (
	"context"
	"fmt"
	"strings"

	"perkeep.org/pkg/blob"
)

// MinRefPrefixDigits is the minimum number of hex digits in an
// abbreviated blobref that ResolveRef expands.
const MinRefPrefixDigits = 4

// ResolveRef parses s as a blobref. If s is a full blobref, it's returned
// without contacting the server. If s is an abbreviated blobref (see
// blob.IsPrefix) with at least MinRefPrefixDigits hex digits, ResolveRef
// asks the server for the blob it abbreviates, returning an error if
// there's no such blob or more than one.
func (c *Client) ResolveRef(ctx context.Context, s string) (blob.Ref, error) {
	if br, ok := blob.Parse(s); ok {
		return br, nil
	}
	if !blob.IsPrefix(s) {
		return blob.Ref{}, fmt.Errorf("invalid blobref %q", s)
	}
	if _, hex, _ := strings.Cut(s, "-"); len(hex) < MinRefPrefixDigits {
		return blob.Ref{}, fmt.Errorf("blobref prefix %q is too short; need at least %d hex digits", s, MinRefPrefixDigits)
	}

	// Blobs enumerate in sorted order, and any blob with prefix s
	// sorts after s itself, so the first two blobs after s say whether
	// s matches none, one, or several.
	ch := make(chan blob.SizedRef, 2)
	errc := make(chan error, 1)
	go func() { errc <- c.EnumerateBlobs(ctx, ch, s, 2) }()
	var matches []blob.Ref
	for sb := range ch {
		if strings.HasPrefix(sb.Ref.String(), s) {
			matches = append(matches, sb.Ref)
		}
	}
	if err := <-errc; err != nil {
		return blob.Ref{}, fmt.Errorf("resolving blobref prefix %q: %w", s, err)
	}
	switch len(matches) {
	case 0:
		return blob.Ref{}, fmt.Errorf("no blob matches blobref prefix %q", s)
	case 1:
		return matches[0], nil
	default:
		return blob.Ref{}, fmt.Errorf("blobref prefix %q is ambiguous; it matches %v, %v, and maybe more", s, matches[0], matches[1])
	}
}
