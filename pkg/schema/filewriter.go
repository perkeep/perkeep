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
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

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
	if err := writeFileChunks(ctx, bs, file, r); err != nil {
		return blob.Ref{}, err
	}
	// The file schema blob goes last, after all its parts.
	json := file.Blob().JSON()
	br := blob.RefFromString(json)
	if err := uploadBlob(ctx, bs, br, []byte(json)); err != nil {
		return blob.Ref{}, err
	}
	return br, nil
}

// WriteFileChunks uploads chunks of r to bs while populating file.
// It does not upload file.
func WriteFileChunks(ctx context.Context, bs blobserver.StatReceiver, file *Builder, r io.Reader) error {
	return writeFileChunks(ctx, bs, file, r)
}

func serverHasBlob(ctx context.Context, bs blobserver.BlobStatter, br blob.Ref) (have bool, err error) {
	_, err = blobserver.StatBlob(ctx, bs, br)
	if err == nil {
		have = true
	} else if errors.Is(err, os.ErrNotExist) {
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
	if errors.Is(err, io.EOF) {
		r.sawEOF = true
	}
	return
}

// uploadBlob uploads data, of ref br, to bs, unless bs already has it.
func uploadBlob(ctx context.Context, bs blobserver.StatReceiver, br blob.Ref, data []byte) error {
	hasIt, err := serverHasBlob(ctx, bs, br)
	if err != nil || hasIt {
		return err
	}
	_, err = blobserver.ReceiveNoHash(ctx, bs, br, bytes.NewReader(data))
	return err
}

// chunksInFlight is how many blobs of a file writeFileChunks uploads at
// once. At ~64 KB chunks, this is ~2MB memory per file.
const chunksInFlight = 32

// writeFileChunks uploads the chunks of r to bs, and then the "bytes"
// schema blobs of its parts, which it sets in file.
func writeFileChunks(ctx context.Context, bs blobserver.StatReceiver, file *Builder, r io.Reader) error {
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(chunksInFlight)
	fc, err := CutFile(r, func(br blob.Ref, data []byte) error {
		if err := gctx.Err(); err != nil {
			return err // an upload failed
		}
		g.Go(func() error { return uploadBlob(gctx, bs, br, data) })
		return nil
	})
	if werr := g.Wait(); werr != nil {
		return werr
	}
	if err != nil {
		return err
	}
	bytesBlobs, err := fc.Populate(file)
	if err != nil {
		return err
	}
	g, gctx = errgroup.WithContext(ctx)
	g.SetLimit(chunksInFlight)
	for _, b := range bytesBlobs {
		g.Go(func() error { return uploadBlob(gctx, bs, b.BlobRef(), []byte(b.JSON())) })
	}
	return g.Wait()
}
