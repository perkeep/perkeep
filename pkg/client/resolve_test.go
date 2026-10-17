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
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver/memory"
)

func TestResolveRef(t *testing.T) {
	ctx := context.Background()
	sto := new(memory.Storage)
	c, err := New(OptionUseStorageClient(sto))
	if err != nil {
		t.Fatal(err)
	}
	add := func(s string) blob.Ref {
		sb, err := sto.ReceiveBlob(ctx, blob.RefFromString(s), strings.NewReader(s))
		if err != nil {
			t.Fatal(err)
		}
		return sb.Ref
	}

	// Find two contents whose blobrefs share a prefix of
	// MinRefPrefixDigits hex digits, so that prefix is ambiguous.
	prefixLen := func(br blob.Ref) int { return len(br.HashName()) + 1 + MinRefPrefixDigits }
	var twin1, twin2 blob.Ref
	seen := map[string]string{}
	for i := 0; !twin2.Valid(); i++ {
		s := fmt.Sprint("blob", i)
		br := blob.RefFromString(s)
		p := br.String()[:prefixLen(br)]
		if prev, ok := seen[p]; ok {
			twin1, twin2 = add(prev), add(s)
		}
		seen[p] = s
	}
	ambiguous := twin1.String()[:prefixLen(twin1)]

	other := add("some other blob")
	full := other.String()
	unique := full[:prefixLen(other)]
	if strings.HasPrefix(twin1.String(), unique) {
		t.Fatalf("test setup: %q unexpectedly also prefixes %v", unique, twin1)
	}
	name := other.HashName()

	tests := []struct {
		in      string
		want    blob.Ref
		wantErr string
	}{
		{in: full, want: other},
		{in: unique, want: other},
		{in: full[:len(full)-1], want: other},
		{in: ambiguous, wantErr: "ambiguous"},
		{in: name + "-" + strings.Repeat("0", MinRefPrefixDigits-1), wantErr: "too short"},
		{in: name + "-" + strings.Repeat("f", 20), wantErr: "no blob matches"},
		{in: "not-a-ref", wantErr: "invalid blobref"},
	}
	for _, tt := range tests {
		got, err := c.ResolveRef(ctx, tt.in)
		if tt.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("ResolveRef(%q) = %v, %v; want error containing %q", tt.in, got, err, tt.wantErr)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("ResolveRef(%q) = %v, %v; want %v", tt.in, got, err, tt.want)
		}
	}
}
