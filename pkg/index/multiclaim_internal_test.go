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
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
)

func TestMultiClaimKeyCompatibility(t *testing.T) {
	pn := blob.RefFromString("pn")
	claim := blob.RefFromString("envelope")
	signer := blob.RefFromString("signer")
	target := blob.RefFromString("target")
	date := "2013-02-24T17:20:03.9212Z"
	primary := keyPermanodeClaim.Key(pn, "KEY", date, claim)
	value := keyPermanodeClaim.Val("set-attribute", "title", "hello", signer)
	suffixes := []struct {
		suffix string
		index  int
		valid  bool
	}{
		{"", 0, true}, {"|00000000000000000000", 0, true}, {"|00000000000000000012", 12, true},
		{"|12", 12, true}, {"|", 0, false}, {"|-1", 0, false}, {"|+1", 0, false},
		{"| 1", 0, false}, {"|1x", 0, false}, {"|999999999999999999999999999999", 0, false}, {"|1|2", 0, false},
	}
	for _, tc := range suffixes {
		t.Run(tc.suffix, func(t *testing.T) {
			got, ok := kvClaim(primary+tc.suffix, value, blob.Parse)
			c := newLoggingCorpus(t)
			cached, cok := c.kvClaimBytes([]byte(primary+tc.suffix), []byte(value))
			if ok != tc.valid || cok != tc.valid {
				t.Fatalf("primary suffix %q accepted: %v/%v; want %v", tc.suffix, ok, cok, tc.valid)
			}
			if ok && (got != cached || got.MultiIndex != tc.index || got.BlobRef != claim) {
				t.Errorf("primary decoding lost identity: %+v / %+v", got, cached)
			}
			secondary := []struct {
				name, key, value string
				parse            func(string, string) bool
			}{
				{"signerattrvalue", keySignerAttrValue.Key("KEY", "title", "hello", date, claim), keySignerAttrValue.Val(pn), func(k, v string) bool {
					cl, ok := newLoggingIndex(t).kvSignerAttrValue(k, v)
					if ok && (cl.BlobRef != claim || cl.MultiIndex != tc.index) {
						t.Errorf("signerattr lost identity: %+v", cl)
					}
					return ok
				}},
				{"pathback", keyPathBackward.Key("KEY", target, claim), keyPathBackward.Val(date, pn, "Y", "suffix"), func(k, v string) bool {
					p, childIndex, ok, _ := newLoggingIndex(t).kvPathBackward(k, v)
					if ok && (p.Claim != claim || childIndex != tc.index) {
						t.Errorf("pathback identity = %v, child index %d; want %v, %d", p.Claim, childIndex, claim, tc.index)
					}
					return ok
				}},
				{"pathforward", keyPathForward.Key("KEY", pn, "suffix", date, claim), keyPathForward.Val("Y", target), func(k, v string) bool {
					p, childIndex, ok, _ := newLoggingIndex(t).kvPathForward(k, v)
					if ok && (p.Claim != claim || childIndex != tc.index) {
						t.Errorf("pathforward identity = %v, child index %d; want %v, %d", p.Claim, childIndex, claim, tc.index)
					}
					return ok
				}},
				{"edgeback", keyEdgeBackward.Key(target, pn, claim), keyEdgeBackward.Val("permanode", ""), func(k, v string) bool {
					p, ok := newLoggingIndex(t).kvEdgeBackward(k, v)
					if ok && p.BlobRef != claim {
						t.Errorf("edgeback reference = %v", p.BlobRef)
					}
					return ok
				}},
			}
			for _, row := range secondary {
				if ok := row.parse(row.key+tc.suffix, row.value); ok != tc.valid {
					t.Errorf("%s suffix %q accepted: %v; want %v", row.name, tc.suffix, ok, tc.valid)
				}
			}
			recent := keyRecentPermanode.Key("KEY", date, claim)
			var buf [4]string
			parts, index, ok := appendClaimKeyParts(buf[:0], recent+tc.suffix, 4)
			if ok != tc.valid || ok && (index != tc.index || parts[3] != claim.String() || strings.Contains(parts[3], "|")) {
				t.Errorf("recent decoding = %v %d %v", parts, index, ok)
			}
		})
	}
}

func TestAppendClaimKeyParts(t *testing.T) {
	const key = "kind|owner|date|claim"
	fields := []string{"kind", "owner", "date", "claim"}
	maxIndex := int(^uint(0) >> 1)
	tests := []struct {
		name  string
		key   string
		n     int
		want  []string // nil means invalid
		index int
	}{
		{name: "legacy", key: key, n: 4, want: fields},
		{name: "multi", key: key + "|00000000000000000012", n: 4, want: fields, index: 12},
		{name: "zero index", key: key + "|0", n: 4, want: fields},
		{name: "padded zero index", key: key + "|" + strings.Repeat("0", 32), n: 4, want: fields},
		{name: "max index", key: key + "|" + strconv.Itoa(maxIndex), n: 4, want: fields, index: maxIndex},
		{name: "padded max index", key: key + "|" + strings.Repeat("0", 32) + strconv.Itoa(maxIndex), n: 4, want: fields, index: maxIndex},
		{name: "single field", key: "field", n: 1, want: []string{"field"}},
		{name: "single field multi", key: "field|7", n: 1, want: []string{"field"}, index: 7},
		{name: "empty key field", key: "", n: 1, want: []string{""}},
		{name: "empty fields", key: "|owner||", n: 4, want: []string{"", "owner", "", ""}},
		{name: "empty fields multi", key: "kind|||claim|12", n: 4, want: []string{"kind", "", "", "claim"}, index: 12},
		{name: "empty key too short", key: "", n: 4},
		{name: "too few fields", key: "kind|owner|date", n: 4},
		{name: "excess fields", key: key + "|12|extra", n: 4},
		{name: "many excess fields", key: key + strings.Repeat("|7", 4096), n: 4},
		{name: "empty index", key: key + "|", n: 4},
		{name: "trailing separator after index", key: key + "|12|", n: 4},
		{name: "negative index", key: key + "|-1", n: 4},
		{name: "signed index", key: key + "|+1", n: 4},
		{name: "space in index", key: key + "| 1", n: 4},
		{name: "nondigit index", key: key + "|1x", n: 4},
		{name: "index just above max", key: key + "|" + strconv.FormatUint(uint64(maxIndex)+1, 10), n: 4},
		{name: "overflow index", key: key + "|" + strconv.Itoa(maxIndex) + "0", n: 4},
		{name: "zero fields", key: key, n: 0},
		{name: "negative fields", key: key, n: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Supply exactly n free slots: an ordinal must not be appended.
			base := make([]string, 2, 2+max(tt.n, 0))
			base[0], base[1] = "prefix", "untouched"
			parts, index, ok := appendClaimKeyParts(base, tt.key, tt.n)
			want := []string{"prefix", "untouched"}
			if tt.want != nil {
				want = append(want, tt.want...)
			}
			if ok != (tt.want != nil) || index != tt.index || !slices.Equal(parts, want) {
				t.Fatalf("appendClaimKeyParts = %q, %d, %v; want %q, %d, %v", parts, index, ok, want, tt.index, tt.want != nil)
			}
			if !slices.Equal(base, want[:2]) {
				t.Errorf("base prefix changed to %q", base)
			}
			if &parts[0] != &base[0] || cap(parts) != cap(base) {
				t.Errorf("provided capacity was not reused: capacity %d; want %d", cap(parts), cap(base))
			}
		})
	}
}

func TestAppendClaimKeyPartsStorage(t *testing.T) {
	const key = "kind|owner|date|claim|12"
	var buf [4]string
	tests := []struct {
		name string
		base []string
		want []string
	}{
		{name: "nil base", want: []string{"kind", "owner", "date", "claim"}},
		{name: "empty buffered base", base: buf[:0], want: []string{"kind", "owner", "date", "claim"}},
		{name: "insufficient capacity", base: make([]string, 1, 2), want: []string{"prefix", "kind", "owner", "date", "claim"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if len(tt.base) != 0 {
				tt.base[0] = "prefix"
			}
			parts, index, ok := appendClaimKeyParts(tt.base, key, 4)
			if !ok || index != 12 || !slices.Equal(parts, tt.want) {
				t.Fatalf("appendClaimKeyParts = %q, %d, %v; want %q, 12, true", parts, index, ok, tt.want)
			}
			if len(tt.base) != 0 && tt.base[0] != "prefix" {
				t.Errorf("base prefix changed to %q", tt.base)
			}
			if tt.name == "empty buffered base" && &parts[0] != &buf[0] {
				t.Error("provided empty buffer was not reused")
			}
		})
	}
	for _, key := range []string{"kind", "kind|owner|date|claim|bad"} {
		if parts, index, ok := appendClaimKeyParts(nil, key, 4); parts != nil || index != 0 || ok {
			t.Errorf("invalid key with nil base = %q, %d, %v; want nil, 0, false", parts, index, ok)
		}
	}
}

// Keep results observable without making the scratch slices escape.
var appendClaimKeyPartsSink int

func TestAppendClaimKeyPartsAllocs(t *testing.T) {
	const key = "kind|owner|date|claim"
	tests := []struct {
		name string
		key  string
		n    int
	}{
		{"legacy", key, 4},
		{"multi", key + "|00000000000000000012", 4},
		{"too few fields", "kind|owner|date", 4},
		{"excess fields", key + "|12|extra", 4},
		{"many excess fields", key + strings.Repeat("|7", 4096), 4},
		{"empty index", key + "|", 4},
		{"trailing separator", key + "|12|", 4},
		{"nondigit index", key + "|1x", 4},
		{"index just above max", key + "|" + strconv.FormatUint(uint64(^uint(0)>>1)+1, 10), 4},
		{"overflow index", key + "|" + strconv.Itoa(int(^uint(0)>>1)) + "0", 4},
		{"zero fields", key, 0},
		{"negative fields", key, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allocs := testing.AllocsPerRun(100, func() {
				var buf [6]string
				buf[0], buf[1] = "prefix", "untouched"
				parts, index, ok := appendClaimKeyParts(buf[:2], tt.key, tt.n)
				sum := len(parts) + index
				for _, part := range parts {
					sum += len(part)
				}
				if ok {
					sum++
				}
				appendClaimKeyPartsSink = sum
			})
			if allocs != 0 {
				t.Errorf("allocations per call = %g; want 0", allocs)
			}
		})
	}
}

func BenchmarkAppendClaimKeyParts(b *testing.B) {
	const key = "recpn|KEY|rt7986-97-75T82:79:96Z|sha1-0123456789abcdef0123456789abcdef01234567"
	for _, tc := range []struct{ name, key string }{
		{"legacy", key},
		{"multi", key + "|00000000000000000012"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.Run("append", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					var buf [4]string
					parts, index, _ := appendClaimKeyParts(buf[:0], tc.key, 4)
					sum := len(parts) + index
					for _, part := range parts {
						sum += len(part)
					}
					appendClaimKeyPartsSink = sum
				}
			})
			// Reproduce the old helper's Split allocation for comparison.
			b.Run("split", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					parts := strings.Split(tc.key, "|")
					index := 0
					if len(parts) == 5 {
						index, _ = parseClaimIndex(parts[4])
						parts = parts[:4]
					}
					sum := len(parts) + index
					for _, part := range parts {
						sum += len(part)
					}
					appendClaimKeyPartsSink = sum
				}
			})
		})
	}
}

// Benchmark the mutation-application stage independently of signing/parsing.
// Map iteration deliberately supplies child rows in an arbitrary order.
func BenchmarkMultiClaimBatch(b *testing.B) {
	pn, claim, signer := blob.RefFromString("pn"), blob.RefFromString("multi"), blob.RefFromString("signer")
	for _, n := range []int{100, 1000} {
		mm := &mutationMap{kv: make(map[string]string), signerID: "KEY", signerBlobRef: signer}
		for i := 0; i < n; i++ {
			key := fmt.Sprintf("%s|%020d", keyPermanodeClaim.Key(pn, "KEY", "2013-02-24T17:20:03Z", claim), i)
			mm.Set(key, keyPermanodeClaim.Val("set-attribute", "title", strconv.Itoa(i), signer))
		}
		for _, corpus := range []bool{false, true} {
			b.Run(fmt.Sprintf("children=%d/corpus=%v", n, corpus), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if corpus {
						c := newCorpus()
						if err := c.addBlob(context.Background(), claim, mm); err != nil {
							b.Fatal(err)
						}
						if got := c.PermanodeAttrValue(pn, "title", time.Time{}, ""); got != strconv.Itoa(n-1) {
							b.Fatalf("final title = %q", got)
						}
					} else {
						ix := NewMemoryIndex()
						if err := ix.commit(mm); err != nil {
							b.Fatal(err)
						}
						ix.Close()
					}
				}
			})
		}
	}
}
