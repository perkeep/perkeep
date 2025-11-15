/*
Copyright 2011 The Perkeep Authors

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
	"context"
	"fmt"
	"io"
	"iter"
	"os"
	"strings"
	"time"

	"github.com/bobg/hashsplit/v2"
	"golang.org/x/sync/errgroup"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
)

const (
	// maxBlobSize is the largest blob we ever make when cutting up
	// a file.
	maxBlobSize = 1 << 20

	// firstChunkSize is the ideal size of the first chunk of a
	// file.  It's kept smaller for the file(1) command, which
	// likes to read 96 kB on Linux and 256 kB on OS X.  Related
	// are tools which extract the EXIF metadata from JPEGs,
	// ID3 from mp3s, etc.  Nautilus, OS X Finder, etc.
	// The first chunk may be larger than this if cutting the file
	// here would create a small subsequent chunk (e.g. a file one
	// byte larger than firstChunkSize)
	firstChunkSize = 256 << 10

	// bufioReaderSize is an explicit size for our bufio.Reader,
	// so we don't rely on NewReader's implicit size.
	// We care about the buffer size because it affects how far
	// in advance we can detect EOF from an io.Reader that doesn't
	// know its size.  Detecting an EOF bufioReaderSize bytes early
	// means we can plan for the final chunk.
	bufioReaderSize = 32 << 10

	// tooSmallThreshold is the threshold at which rolling checksum
	// boundaries are ignored if the current chunk being built is
	// smaller than this.
	tooSmallThreshold = 64 << 10
)

// WriteFileFromReaderWithModTime creates and uploads a "file" JSON schema
// composed of chunks of r, also uploading the chunks.  The returned
// BlobRef is of the JSON file schema blob.
// Both filename and modTime are optional.
func WriteFileFromReaderWithModTime(ctx context.Context, bs blobserver.StatReceiver, filename string, modTime time.Time, r io.Reader) (blob.Ref, error) {
	if strings.Contains(filename, "/") {
		return blob.Ref{}, fmt.Errorf("schema.WriteFileFromReader: filename %q shouldn't contain a slash", filename)
	}

	m := NewFileMap(filename)
	if !modTime.IsZero() {
		m.SetModTime(modTime)
	}
	return WriteFileMap(ctx, bs, m, r)
}

// WriteFileFromReader creates and uploads a "file" JSON schema
// composed of chunks of r, also uploading the chunks.  The returned
// BlobRef is of the JSON file schema blob.
// The filename is optional.
func WriteFileFromReader(ctx context.Context, bs blobserver.StatReceiver, filename string, r io.Reader) (blob.Ref, error) {
	return WriteFileFromReaderWithModTime(ctx, bs, filename, time.Time{}, r)
}

// WriteFileMap uploads chunks of r to bs while populating file and
// finally uploading file's Blob. The returned blobref is of file's
// JSON blob.
func WriteFileMap(ctx context.Context, bs blobserver.StatReceiver, file *Builder, r io.Reader) (blob.Ref, error) {
	return writeFileMapRolling(ctx, bs, file, r)
}

func serverHasBlob(ctx context.Context, bs blobserver.BlobStatter, br blob.Ref) (have bool, err error) {
	_, err = blobserver.StatBlob(ctx, bs, br)
	if err == nil {
		have = true
	} else if err == os.ErrNotExist {
		err = nil
	}
	return
}

type span struct {
	from, to int64
	bits     int
	br       blob.Ref
	children []span
}

func (s *span) isSingleBlob() bool {
	return len(s.children) == 0
}

func (s *span) size() int64 {
	size := s.to - s.from
	for _, cs := range s.children {
		size += cs.size()
	}
	return size
}

// noteEOFReader keeps track of when it's seen EOF, but otherwise
// delegates entirely to r.
type noteEOFReader struct {
	r      io.Reader
	sawEOF bool
}

func (r *noteEOFReader) Read(p []byte) (n int, err error) {
	n, err = r.r.Read(p)
	if err == io.EOF {
		r.sawEOF = true
	}
	return
}

func uploadString(ctx context.Context, bs blobserver.StatReceiver, br blob.Ref, s string) (blob.Ref, error) {
	if !br.Valid() {
		panic("invalid blobref")
	}
	hasIt, err := serverHasBlob(ctx, bs, br)
	if err != nil {
		return blob.Ref{}, err
	}
	if hasIt {
		return br, nil
	}
	_, err = blobserver.ReceiveNoHash(ctx, bs, br, strings.NewReader(s))
	if err != nil {
		return blob.Ref{}, err
	}
	return br, nil
}

// uploadBytes populates bb (a builder of either type "bytes" or
// "file", which is a superset of "bytes"), sets it to the provided
// size, and populates with provided spans.  The bytes or file schema
// blob is uploaded and its blobref is returned.
func uploadBytes(ctx context.Context, bs blobserver.StatReceiver, bb *Builder, size int64, s []span) *uploadBytesFuture {
	future := newUploadBytesFuture()
	parts := []BytesPart{}
	addBytesParts(ctx, bs, &parts, s, future)

	if err := bb.PopulateParts(size, parts); err != nil {
		future.errc <- err
		return future
	}

	// Hack until perkeep.org/issue/102 is fixed. If we happen to upload
	// the "file" schema before any of its parts arrive, then the indexer
	// can get confused.  So wait on the parts before, and then upload
	// the "file" blob afterwards.
	if bb.Type() == TypeFile {
		future.errc <- nil
		_, err := future.Get() // may not be nil, if children parts failed
		future = newUploadBytesFuture()
		if err != nil {
			future.errc <- err
			return future
		}
	}

	json := bb.Blob().JSON()
	br := blob.RefFromString(json)
	future.br = br
	go func() {
		_, err := uploadString(ctx, bs, br, json)
		future.errc <- err
	}()
	return future
}

func newUploadBytesFuture() *uploadBytesFuture {
	return &uploadBytesFuture{
		errc: make(chan error, 1),
	}
}

// An uploadBytesFuture is an eager result of a still-in-progress uploadBytes call.
// Call Get to wait and get its final result.
type uploadBytesFuture struct {
	br       blob.Ref
	errc     chan error
	children []*uploadBytesFuture
}

// BlobRef returns the optimistic blobref of this uploadBytes call without blocking.
func (f *uploadBytesFuture) BlobRef() blob.Ref {
	return f.br
}

// Get blocks for all children and returns any final error.
func (f *uploadBytesFuture) Get() (blob.Ref, error) {
	for _, f := range f.children {
		if _, err := f.Get(); err != nil {
			return blob.Ref{}, err
		}
	}
	return f.br, <-f.errc
}

// addBytesParts uploads the provided spans to bs, appending elements to *dst.
func addBytesParts(ctx context.Context, bs blobserver.StatReceiver, dst *[]BytesPart, spans []span, parent *uploadBytesFuture) {
	for _, sp := range spans {
		if len(sp.children) == 1 && sp.children[0].isSingleBlob() {
			// Remove an occasional useless indirection of
			// what would become a bytes schema blob
			// pointing to a single blobref.  Just promote
			// the blobref child instead.
			child := sp.children[0]
			*dst = append(*dst, BytesPart{
				BlobRef: child.br,
				Size:    uint64(child.size()),
			})
			sp.children = nil
		}
		if len(sp.children) > 0 {
			childrenSize := int64(0)
			for _, cs := range sp.children {
				childrenSize += cs.size()
			}
			future := uploadBytes(ctx, bs, newBytes(), childrenSize, sp.children)
			parent.children = append(parent.children, future)
			*dst = append(*dst, BytesPart{
				BytesRef: future.BlobRef(),
				Size:     uint64(childrenSize),
			})
		}
		if sp.from == sp.to {
			panic("Shouldn't happen. " + fmt.Sprintf("weird span with same from & to: %#v", sp))
		}
		*dst = append(*dst, BytesPart{
			BlobRef: sp.br,
			Size:    uint64(sp.to - sp.from),
		})
	}
}

// writeFileMap uploads chunks of r to bs while populating fileMap and
// finally uploading fileMap. The returned blobref is of fileMap's
// JSON blob. It uses rolling checksum for the chunks sizes.
func writeFileMapRolling(ctx context.Context, bs blobserver.StatReceiver, file *Builder, r io.Reader) (blob.Ref, error) {
	n, spans, err := writeFileChunks(ctx, bs, r)
	if err != nil {
		return blob.Ref{}, err
	}
	// The top-level content parts
	return uploadBytes(ctx, bs, file, n, spans).Get()
}

// WriteFileChunks uploads chunks of r to bs while populating file.
// It does not upload file.
func WriteFileChunks(ctx context.Context, bs blobserver.StatReceiver, file *Builder, r io.Reader) error {
	size, spans, err := writeFileChunks(ctx, bs, r)
	if err != nil {
		return err
	}
	parts := []BytesPart{}
	future := newUploadBytesFuture()
	addBytesParts(ctx, bs, &parts, spans, future)
	future.errc <- nil // Get will still block on addBytesParts' children
	if _, err := future.Get(); err != nil {
		return err
	}
	return file.PopulateParts(size, parts)
}

func writeFileChunks(ctx context.Context, bs blobserver.StatReceiver, r io.Reader) (n int64, spans []span, outerr error) {
	src := &noteEOFReader{r: r}
	bufr := bufio.NewReaderSize(src, bufioReaderSize)
	spans = []span{} // the tree of spans, cut on interesting rollsum boundaries
	var nbytes int64

	spl := hashsplit.NewSplitter()
	spl.SplitBits = 16 // A chunk boundary occurs on average once every 64k bytes; median chunk size is 45426.
	spl.MinSize = tooSmallThreshold

	const chunksInFlight = 32 // at ~64 KB chunks, this is ~2MB memory per file
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(chunksInFlight)

	chunkPairs, errptr := spl.Split(bufr)
	chunkPairs = adjustChunks(chunkPairs)

	for chunkBytes, bits := range chunkPairs {
		chunkStr := string(chunkBytes)
		br := blob.RefFromString(chunkStr)

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
		to := nbytes + int64(len(chunkBytes))
		spans = append(spans, span{from: nbytes, to: to, bits: bits, children: children})
		nbytes = to

		g.Go(func() error {
			_, err := uploadString(ctx, bs, br, chunkStr)
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return 0, nil, fmt.Errorf("writeFileChunks: uploading chunks: %w", err)
	}
	if err := *errptr; err != nil {
		return 0, nil, fmt.Errorf("writeFileChunks: reading input: %w", err)
	}

	return nbytes, spans, nil
}

func adjustChunks(chunks iter.Seq2[[]byte, int]) iter.Seq2[[]byte, int] {
	return func(yield func(chunk []byte, level int) bool) {
		next, stop := iter.Pull2(chunks)
		defer stop()

		// Produce the first chunk pair.
		var (
			firstChunk []byte
			firstLevel = -1
		)
		for {
			chunk, level, ok := next()
			if !ok {
				if firstLevel >= 0 {
					yield(firstChunk, firstLevel)
				}
				return
			}

			// Optimization.
			if len(firstChunk) == 0 && len(chunk) >= firstChunkSize {
				// Just use this chunk as-is.
				if !yield(chunk, level) {
					return
				}
				break
			}

			if level > firstLevel {
				firstLevel = level
			}
			firstChunk = append(firstChunk, chunk...)

			if len(firstChunk) >= firstChunkSize {
				if !yield(firstChunk, firstLevel) {
					return
				}
				break
			}
		}

		// Produce the rest of the chunks.
		for {
			chunk, level, ok := next()
			if !ok || !yield(chunk, level) {
				return
			}
		}
	}
}
