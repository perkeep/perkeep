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

package schema

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"

	"go4.org/rollsum"

	"perkeep.org/pkg/blob"
)

// A FileCut is a file's contents cut into chunks, by CutFile.
type FileCut struct {
	size  int64
	spans []span // the tree of spans, cut on interesting rollsum boundaries
}

// Size returns the size of the contents that were cut.
func (fc *FileCut) Size() int64 { return fc.size }

// CutFile reads r to its end, cutting its contents into chunks where
// WriteFileMap does, and calls chunk with each chunk's ref and contents,
// in order. chunk may keep data. If chunk returns an error, CutFile stops
// and returns it.
//
// The FileCut it returns says how the chunks make up the contents, for
// Populate to describe them in a file schema blob.
func CutFile(r io.Reader, chunk func(br blob.Ref, data []byte) error) (*FileCut, error) {
	src := &noteEOFReader{r: r}
	bufr := bufio.NewReaderSize(src, bufioReaderSize)
	spans := []span{}
	rs := rollsum.New()
	var n, last int64
	var buf bytes.Buffer
	blobSize := 0 // of the next blob being built, should be same as buf.Len()

	// emitLastSpan passes the contents of buf, the last span, to chunk.
	emitLastSpan := func() error {
		data := bytes.Clone(buf.Bytes())
		buf.Reset()
		br := blob.RefFromBytes(data)
		spans[len(spans)-1].br = br
		return chunk(br, data)
	}

	for {
		c, err := bufr.ReadByte()
		if errors.Is(err, io.EOF) {
			if n != last {
				spans = append(spans, span{from: last, to: n})
				if err := emitLastSpan(); err != nil {
					return nil, err
				}
			}
			break
		}
		if err != nil {
			return nil, err
		}

		buf.WriteByte(c)
		n++
		blobSize++
		rs.Roll(c)

		var bits int
		onRollSplit := rs.OnSplit()
		switch {
		case blobSize == maxBlobSize:
			bits = 20 // arbitrary node weight; 1<<20 == 1MB
		case src.sawEOF:
			// Don't split. End is coming soon enough.
			continue
		case onRollSplit && n > firstChunkSize && blobSize > tooSmallThreshold:
			bits = rs.Bits()
		case n == firstChunkSize:
			bits = 18 // 1 << 18 == 256KB
		default:
			// Don't split.
			continue
		}
		blobSize = 0

		// Take any spans from the end of the spans slice that
		// have a smaller 'bits' score and make them children
		// of this node.
		var children []span
		childrenFrom := len(spans)
		for childrenFrom > 0 && spans[childrenFrom-1].bits < bits {
			childrenFrom--
		}
		if nCopy := len(spans) - childrenFrom; nCopy > 0 {
			children = make([]span, nCopy)
			copy(children, spans[childrenFrom:])
			spans = spans[:childrenFrom]
		}

		spans = append(spans, span{from: last, to: n, bits: bits, children: children})
		last = n
		if err := emitLastSpan(); err != nil {
			return nil, err
		}
	}
	return &FileCut{size: n, spans: spans}, nil
}

// Populate sets the parts of file, a builder of a "file" or "bytes"
// schema blob, to the contents that were cut. It returns the "bytes"
// schema blobs the parts need, which refer to chunks and to each other;
// each comes after the bytes blobs it refers to.
func (fc *FileCut) Populate(file *Builder) ([]*Blob, error) {
	var blobs []*Blob
	parts, err := bytesParts(fc.spans, &blobs)
	if err != nil {
		return nil, err
	}
	if err := file.PopulateParts(fc.size, parts); err != nil {
		return nil, err
	}
	return blobs, nil
}

// bytesParts returns the parts describing spans, appending to *blobs the
// "bytes" schema blobs they need.
func bytesParts(spans []span, blobs *[]*Blob) ([]BytesPart, error) {
	parts := []BytesPart{}
	for _, sp := range spans {
		children := sp.children
		if len(children) == 1 && children[0].isSingleBlob() {
			// Remove an occasional useless indirection of
			// what would become a bytes schema blob
			// pointing to a single blobref.  Just promote
			// the blobref child instead.
			child := children[0]
			parts = append(parts, BytesPart{
				BlobRef: child.br,
				Size:    uint64(child.size()),
			})
			children = nil
		}
		if len(children) > 0 {
			childrenSize := int64(0)
			for _, cs := range children {
				childrenSize += cs.size()
			}
			childParts, err := bytesParts(children, blobs)
			if err != nil {
				return nil, err
			}
			bb := newBytes()
			if err := bb.PopulateParts(childrenSize, childParts); err != nil {
				return nil, err
			}
			b := bb.Blob()
			*blobs = append(*blobs, b)
			parts = append(parts, BytesPart{
				BytesRef: b.BlobRef(),
				Size:     uint64(childrenSize),
			})
		}
		if sp.from == sp.to {
			panic("Shouldn't happen. " + fmt.Sprintf("weird span with same from & to: %#v", sp))
		}
		parts = append(parts, BytesPart{
			BlobRef: sp.br,
			Size:    uint64(sp.to - sp.from),
		})
	}
	return parts, nil
}
