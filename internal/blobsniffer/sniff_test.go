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

package blobsniffer

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
)

func TestBlobSnifferRetention(t *testing.T) {
	data := bytes.Repeat([]byte("x"), schema.MaxSchemaBlobSize+1)
	for _, private := range []bool{false, true} {
		for _, size := range []int{0, 1, schema.MaxSchemaBlobSize - 1, schema.MaxSchemaBlobSize, schema.MaxSchemaBlobSize + 1} {
			t.Run(fmt.Sprintf("private=%v/size=%d", private, size), func(t *testing.T) {
				sn := New(blob.RefFromBytes(data[:size]))
				if private {
					sn = Get(blob.RefFromBytes(data[:size]))
					defer Put(sn)
				}
				for _, p := range [][]byte{data[:min(7, size)], nil, data[min(7, size):size], nil} {
					if n, err := sn.Write(p); n != len(p) || err != nil {
						t.Fatalf("Write = %d, %v; want %d, nil", n, err, len(p))
					}
				}
				truncated := size > schema.MaxSchemaBlobSize
				if sn.Size() != int64(size) || sn.IsTruncated() != truncated ||
					!bytes.Equal(sn.contents, data[:min(size, schema.MaxSchemaBlobSize)]) {
					t.Fatal("incorrect retained prefix, size, or truncation")
				}
				body, err := sn.Body()
				if truncated {
					if body != nil || err == nil || err.Error() != "index.Body: was truncated" {
						t.Fatal("Body of truncated sniffer must fail")
					}
				} else if err != nil || !bytes.Equal(body, data[:size]) {
					t.Fatalf("Body = %d bytes, %v; want %d bytes, nil", len(body), err, size)
				}
			})
		}
	}
}

func TestPublicBlobSnifferBodyOwnership(t *testing.T) {
	data := []byte("public body remains caller-owned")
	public := New(blob.RefFromBytes(data))
	public.Write(data)
	body, err := public.Body()
	if err != nil {
		t.Fatal(err)
	}
	private := Get(blob.RefFromBytes(data))
	defer Put(private)
	private.Write(data)
	clear(private.contents) // Poison while borrowed, never after returning to the pool.
	if !bytes.Equal(body, data) {
		t.Fatal("private sniffer overwrote public Body")
	}
	body[0] = 'P'
	again, err := public.Body()
	if err != nil || again[0] != 'P' {
		t.Fatal("Body must continue to expose the public sniffer's owned slice")
	}
}

func TestSnifferBufferPoolBounds(t *testing.T) {
	for _, capacity := range []int{0, maxPooledSnifferCapacity, maxPooledSnifferCapacity + 1} {
		sn := Get(blob.RefFromString("pooled blob"))
		sn.contents = make([]byte, min(1, capacity), capacity)
		Put(sn)
		borrowed := Get(blob.RefFromString("next blob"))
		// A pool may discard entries: assert invariants, not identity or reuse.
		length, capacity := len(borrowed.contents), cap(borrowed.contents)
		Put(borrowed)
		if length != 0 || capacity > maxPooledSnifferCapacity {
			t.Fatalf("borrowed len/cap = %d/%d; want len 0, cap <= %d", length, capacity, maxPooledSnifferCapacity)
		}
	}
}

func TestParsedSchemaOwnsSnifferData(t *testing.T) {
	ref := blob.RefFromString("schema dependency")
	file := schema.NewFileMap("owned.txt")
	if err := file.PopulateParts(3, []schema.BytesPart{{BlobRef: ref, Size: 3}}); err != nil {
		t.Fatal(err)
	}
	claim := schema.NewSetAttributeClaim(ref, "title", "owned value").
		SetClaimDate(time.Unix(1700000000, 0).UTC()).SetSigner(ref).
		SetRawStringField("camliSig", "owned signature")
	for _, original := range []*schema.Blob{file.Blob(), claim.Blob()} {
		t.Run(string(original.Type()), func(t *testing.T) {
			sn := Get(original.BlobRef())
			defer Put(sn)
			sn.Write([]byte(original.JSON()))
			sn.Parse()
			parsed, ok := sn.SchemaBlob()
			if !ok {
				t.Fatal("schema not recognized")
			}
			c, isClaim := parsed.AsClaim()
			for i := range sn.contents {
				sn.contents[i] = '!' // Deterministic reuse simulation while still owned.
			}
			if parsed.JSON() != original.JSON() || parsed.Type() != original.Type() ||
				parsed.FileName() != original.FileName() || !reflect.DeepEqual(parsed.ByteParts(), original.ByteParts()) {
				t.Fatal("parsed schema retained sniffer backing")
			}
			if original.Type() == schema.TypeClaim {
				if !isClaim || c.ClaimType() != schema.SetAttributeClaim || c.Attribute() != "title" ||
					c.Value() != "owned value" || c.Signature() != "owned signature" ||
					c.Signer() != ref || c.ModifiedPermanode() != ref || c.ClaimDateString() != "2023-11-14T22:13:20Z" {
					t.Fatal("parsed claim retained sniffer backing")
				}
			}
		})
	}
}

func TestParse(t *testing.T) {
	for _, pooled := range []bool{false, true} {
		t.Run(fmt.Sprintf("pooled=%v", pooled), func(t *testing.T) {
			for _, tt := range []struct {
				name      string
				body      string
				mimeType  string
				camliType schema.CamliType
			}{
				{"empty", "", "", ""},
				{"text", "opaque text", "", ""},
				{"binary", "\x00\x01\x02\x03", "", ""},
				{"png", "\x89PNG\r\n\x1a\nimage data", "image/png", ""},
				{"jpeg", "\xff\xd8\xff\xe1image data", "image/jpeg", ""},
				{"json", `{"other": true}`, "", ""},
				{"incompleteSchema", `{"camliVersion": 1, "camliType": "file",`, "", ""},
				{"invalidVersion", `{"camliVersion": "one", "camliType": "file"}`, "", ""},
				{"file", `{"camliVersion":1,"camliType":"file","fileName":"hello.txt","parts":[]}`, "application/json; camliType=file", schema.TypeFile},
				{"bytes", `{"camliVersion":1,"camliType":"bytes","parts":[]}`, "application/json; camliType=bytes", schema.TypeBytes},
				{"staticSet", "{\n \"camliType\": \"static-set\", \"camliVersion\": 1, \"members\": []\n}", "application/json; camliType=static-set", schema.TypeStaticSet},
				{"opaque after schema", "opaque text", "", ""},
			} {
				t.Run(tt.name, func(t *testing.T) {
					ref := blob.RefFromString(tt.body)
					sn := New(ref)
					if pooled {
						sn = Get(ref)
						defer Put(sn)
					}
					if sn.MIMEType() != "" || sn.CamliType() != "" {
						t.Fatal("unparsed sniffer has a type")
					}
					if b, ok := sn.SchemaBlob(); b != nil || ok {
						t.Fatal("unparsed sniffer has schema metadata")
					}
					// Include empty and small writes as well as a complete blob.
					for _, p := range [][]byte{nil, []byte(tt.body[:len(tt.body)/2]), []byte(tt.body[len(tt.body)/2:])} {
						if n, err := sn.Write(p); n != len(p) || err != nil {
							t.Fatalf("Write = %d, %v; want %d, nil", n, err, len(p))
						}
					}
					for range 2 { // Parsing without further writes is idempotent.
						sn.Parse()
						if sn.MIMEType() != tt.mimeType || sn.CamliType() != tt.camliType {
							t.Fatalf("MIMEType/CamliType = %q/%q; want %q/%q", sn.MIMEType(), sn.CamliType(), tt.mimeType, tt.camliType)
						}
						b, ok := sn.SchemaBlob()
						if tt.camliType == "" {
							if b != nil || ok {
								t.Fatal("opaque blob recognized as schema")
							}
						} else if !ok || b.Type() != tt.camliType || b.BlobRef() != ref || b.JSON() != tt.body {
							t.Fatalf("incorrect schema metadata: %v, %v", b, ok)
						}
					}
				})
			}
		})
	}
}

func TestParseSchemaSizeLimit(t *testing.T) {
	for _, pooled := range []bool{false, true} {
		t.Run(fmt.Sprintf("pooled=%v", pooled), func(t *testing.T) {
			prefix := []byte(`{"camliVersion":1,"camliType":"file","fileName":"large.txt","parts":[]}`)
			for _, size := range []int{schema.MaxSchemaBlobSize - 1, schema.MaxSchemaBlobSize, schema.MaxSchemaBlobSize + 1} {
				t.Run(fmt.Sprint(size), func(t *testing.T) {
					data := append(bytes.Clone(prefix), bytes.Repeat([]byte(" "), size-len(prefix))...)
					sn := New(blob.RefFromBytes(data))
					if pooled {
						sn = Get(blob.RefFromBytes(data))
						defer Put(sn)
					}
					sn.Write(data)
					sn.Parse()
					if sn.Size() != int64(size) || sn.IsTruncated() != (size > schema.MaxSchemaBlobSize) {
						t.Fatal("incorrect size or truncation")
					}
					if b, ok := sn.SchemaBlob(); !ok || b.FileName() != "large.txt" || b.JSON() != string(data[:min(size, schema.MaxSchemaBlobSize)]) {
						t.Fatal("retained schema prefix was not parsed intact")
					}
				})
			}
		})
	}
}

func TestWriteCopiesInput(t *testing.T) {
	for _, pooled := range []bool{false, true} {
		t.Run(fmt.Sprintf("pooled=%v", pooled), func(t *testing.T) {
			data := []byte("input must not be retained")
			want := bytes.Clone(data)
			sn := New(blob.RefFromBytes(data))
			if pooled {
				sn = Get(blob.RefFromBytes(data))
				defer Put(sn)
			}
			sn.Write(data)
			clear(data)
			if body, err := sn.Body(); err != nil || !bytes.Equal(body, want) {
				t.Fatalf("Body = %q, %v; want %q, nil", body, err, want)
			}
		})
	}
}

func TestPooledSnifferState(t *testing.T) {
	data := `{"camliVersion":1,"camliType":"file","fileName":"old.txt","parts":[]}`
	sn := Get(blob.RefFromString(data))
	sn.Write([]byte(data))
	sn.Parse()
	if _, ok := sn.SchemaBlob(); !ok {
		t.Fatal("schema not recognized")
	}
	Put(sn)

	ref := blob.RefFromString("next blob")
	sn = Get(ref)
	defer Put(sn)
	// Pools may discard entries; check state, not object identity or reuse.
	if sn.br != ref || sn.Size() != 0 || sn.IsTruncated() || sn.MIMEType() != "" || sn.CamliType() != "" {
		t.Fatal("pooled sniffer retained previous state")
	}
	if b, ok := sn.SchemaBlob(); b != nil || ok {
		t.Fatal("pooled sniffer retained previous schema")
	}
	if body, err := sn.Body(); err != nil || len(body) != 0 {
		t.Fatalf("pooled Body = %q, %v; want empty, nil", body, err)
	}
}

func TestInvalidRef(t *testing.T) {
	for _, tt := range []struct {
		name string
		fn   func()
		want string
	}{
		{"New", func() { New(blob.Ref{}) }, "invalid ref"},
		{"Get", func() { Get(blob.Ref{}) }, "invalid ref"},
		{"Write", func() { new(BlobSniffer).Write(nil) }, "write on sniffer with invalid blobref"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if got := recover(); got != tt.want {
					t.Fatalf("panic = %v; want %q", got, tt.want)
				}
			}()
			tt.fn()
		})
	}
}
