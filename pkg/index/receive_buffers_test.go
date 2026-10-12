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
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/sorted"
)

func TestKeepFirstNSizeHints(t *testing.T) {
	for _, tt := range []struct {
		name string
		n    int
		hint int64
		cap  int
	}{
		{"zero", 8, 0, 0},
		{"negative", 8, -1, 0},
		{"underestimated", 8, 2, 2},
		{"exact", 8, 8, 8},
		{"huge", 8, math.MaxInt64, 8},
		{"noRetention", 0, math.MaxInt64, 0},
		{"negativeN", -1, math.MaxInt64, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := newKeepFirstN(tt.n, tt.hint)
			if len(w.Bytes) != 0 || cap(w.Bytes) != tt.cap || w.N != tt.n {
				t.Fatalf("initial len/cap/N = %d/%d/%d; want 0/%d/%d", len(w.Bytes), cap(w.Bytes), w.N, tt.cap, tt.n)
			}
			var input []byte
			for _, p := range []string{"", "abc", "defghijk", "", "tail"} {
				n, err := w.Write([]byte(p))
				if n != len(p) || err != nil {
					t.Fatalf("Write(%q) = %d, %v; want %d, nil", p, n, err, len(p))
				}
				input = append(input, p...)
				want := input[:max(0, min(tt.n, len(input)))]
				if !bytes.Equal(w.Bytes, want) || w.N != tt.n {
					t.Fatalf("retained %q, N=%d; want %q, N=%d", w.Bytes, w.N, want, tt.n)
				}
			}
		})
	}
}

func TestCopyWithBuffer(t *testing.T) {
	data := bytes.Repeat([]byte("abcdef"), 12000)
	boom := errors.New("copy failed")
	for _, tt := range []struct {
		name string
		src  io.Reader
		dst  io.Writer
		n    int64
		err  error
	}{
		{"plain", struct{ io.Reader }{bytes.NewReader(data)}, new(bytes.Buffer), int64(len(data)), nil},
		{"readError", io.MultiReader(bytes.NewReader(data), bufferErrorReader{boom}), new(bytes.Buffer), int64(len(data)), boom},
		{"writeError", bytes.NewReader(data), bufferFaultWriter{boom}, 16 << 10, boom},
		{"shortWrite", bytes.NewReader(data), bufferFaultWriter{}, 16 << 10, io.ErrShortWrite},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Hide both fast paths to exercise the pooled scratch path.
			n, err := copyWithBuffer(struct{ io.Writer }{tt.dst}, struct{ io.Reader }{tt.src})
			if n != tt.n || !errors.Is(err, tt.err) {
				t.Fatalf("copy = %d, %v; want %d, %v", n, err, tt.n, tt.err)
			}
			if dst, ok := tt.dst.(*bytes.Buffer); ok && !bytes.Equal(dst.Bytes(), data) {
				t.Fatal("copied bytes differ")
			}
		})
	}
}

func TestCopyWithBufferFastPaths(t *testing.T) {
	for _, writerTo := range []bool{false, true} {
		t.Run(fmt.Sprintf("WriterTo=%v", writerTo), func(t *testing.T) {
			wantErr := errors.New("fast path sentinel")
			src := &bufferWriterTo{err: wantErr}
			dst := &bufferReaderFrom{err: wantErr}
			var r io.Reader = src
			if !writerTo {
				r = struct{ io.Reader }{src}
			}
			n, err := copyWithBuffer(dst, r)
			if n != 17 || err != wantErr || src.called != writerTo || dst.called == writerTo {
				t.Fatalf("copy = %d, %v; WriterTo/ReaderFrom called = %v/%v", n, err, src.called, dst.called)
			}
		})
	}
}

func TestBlobSnifferRetention(t *testing.T) {
	data := bytes.Repeat([]byte("x"), schema.MaxSchemaBlobSize+1)
	for _, private := range []bool{false, true} {
		for _, size := range []int{schema.MaxSchemaBlobSize - 1, schema.MaxSchemaBlobSize, schema.MaxSchemaBlobSize + 1} {
			t.Run(fmt.Sprintf("private=%v/size=%d", private, size), func(t *testing.T) {
				sn := NewBlobSniffer(blob.RefFromBytes(data[:size]))
				if private {
					holder := getSnifferBuffer()
					sn.contents = (*holder)[:0]
					defer func() { putSnifferBuffer(holder, sn.contents) }()
				}
				for _, p := range [][]byte{data[:7], nil, data[7:size]} {
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
					if body != nil || err == nil {
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
	public := NewBlobSniffer(blob.RefFromBytes(data))
	public.Write(data)
	body, err := public.Body()
	if err != nil {
		t.Fatal(err)
	}
	private := NewBlobSniffer(blob.RefFromBytes(data))
	holder := getSnifferBuffer()
	private.contents = (*holder)[:0]
	defer func() { putSnifferBuffer(holder, private.contents) }()
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
		// Supply final grown contents, not the holder's original empty slice.
		holder := getSnifferBuffer()
		putSnifferBuffer(holder, make([]byte, min(1, capacity), capacity))
		borrowed := getSnifferBuffer()
		// A pool may discard entries: assert invariants, not identity or reuse.
		length, capacity := len(*borrowed), cap(*borrowed)
		putSnifferBuffer(borrowed, *borrowed)
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
			sn := NewBlobSniffer(original.BlobRef())
			holder := getSnifferBuffer()
			sn.contents = (*holder)[:0]
			defer func() { putSnifferBuffer(holder, sn.contents) }()
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

func newReceiveBufferIndex(tb testing.TB) *Index {
	tb.Helper()
	ix, err := New(sorted.NewMemoryKeyValue())
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		if err := ix.Close(); err != nil {
			tb.Error(err)
		}
	})
	return ix
}

func TestReceiveBlobBuffers(t *testing.T) {
	t.Setenv("CAMLI_REDO_INDEX_ON_RECEIVE", "false")
	ix := newReceiveBufferIndex(t)
	data := bytes.Repeat([]byte("raw data\n"), (schema.MaxSchemaBlobSize/9)+1)
	ref := blob.RefFromBytes(data)
	boom := errors.New("upload failed")
	for _, tt := range []struct {
		name string
		src  io.Reader
		err  error
	}{
		{"readError", io.MultiReader(bytes.NewReader(data), bufferErrorReader{boom}), boom},
		{"corrupt", bytes.NewReader([]byte("wrong digest")), blobserver.ErrCorruptBlob},
		{"success", bytes.NewReader(data), nil},
		{"duplicate", bytes.NewReader(data), nil},
		{"duplicateReadError", io.MultiReader(bytes.NewReader(data), bufferErrorReader{boom}), boom},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sb, err := blobserver.Receive(context.Background(), ix, ref, tt.src)
			if !errors.Is(err, tt.err) {
				t.Fatalf("Receive = %v; want %v", err, tt.err)
			}
			if tt.err == nil && sb != (blob.SizedRef{Ref: ref, Size: uint32(len(data))}) {
				t.Fatalf("Receive = %v; wrong sized ref", sb)
			}
			have, haveErr := ix.s.Get("have:" + ref.String())
			if tt.name == "readError" || tt.name == "corrupt" {
				if !errors.Is(haveErr, sorted.ErrNotFound) {
					t.Fatalf("failed receive indexed blob: %q, %v", have, haveErr)
				}
			} else if want := fmt.Sprintf("%d|indexed", len(data)); haveErr != nil || have != want {
				t.Fatalf("have = %q, %v; want %q, nil", have, haveErr, want)
			}
		})
	}
}

func BenchmarkRawReceive(b *testing.B) {
	b.Setenv("CAMLI_REDO_INDEX_ON_RECEIVE", "true") // Every iteration indexes, including duplicates.
	data := bytes.Repeat([]byte("raw data\n"), (schema.MaxSchemaBlobSize/9)+1)
	ref := blob.RefFromBytes(data)
	for _, readerOnly := range []bool{true, false} {
		name := "bytes.Reader"
		if readerOnly {
			name = "readerOnly"
		}
		b.Run(name, func(b *testing.B) {
			ix := newReceiveBufferIndex(b)
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			for b.Loop() {
				var r io.Reader = bytes.NewReader(data)
				if readerOnly {
					r = struct{ io.Reader }{r}
				}
				if _, err := ix.ReceiveBlob(context.Background(), ref, r); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

type bufferErrorReader struct{ err error }

func (r bufferErrorReader) Read([]byte) (int, error) { return 0, r.err }

type bufferFaultWriter struct{ err error }

func (w bufferFaultWriter) Write(p []byte) (int, error) { return len(p) / 2, w.err }

type bufferWriterTo struct {
	called bool
	err    error
}

func (*bufferWriterTo) Read([]byte) (int, error) { panic("Read bypassed WriterTo/ReaderFrom") }
func (r *bufferWriterTo) WriteTo(io.Writer) (int64, error) {
	r.called = true
	return 17, r.err
}

type bufferReaderFrom struct {
	called bool
	err    error
}

func (*bufferReaderFrom) Write([]byte) (int, error) { panic("Write bypassed fast path") }
func (w *bufferReaderFrom) ReadFrom(io.Reader) (int64, error) {
	w.called = true
	return 17, w.err
}
