/*
Copyright 2014 The Camlistore Authors

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

// Package archiver zips lots of little blobs into bigger zip files
// and stores them somewhere. While generic, it was designed to
// incrementally create Amazon Glacier archives from many little
// blobs, rather than creating millions of Glacier archives.
package archiver

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"golang.org/x/net/context"
)

// DefaultMinZipSize is the default value of Archiver.MinZipSize.
const DefaultMinZipSize = 16 << 20

// An Archiver specifies the parameters of the job that copies from
// one blobserver Storage (the Source) to long-term storage.
type Archiver struct {
	// Source is where the blobs should come from.
	// (and be deleted from, if DeleteSourceAfterStore)
	Source blobserver.Storage

	// MinZipSize is the minimum size of zip files to create.
	// If zero, DefaultMinZipSize is used.
	MinZipSize int64

	// Store specifies a function that writes the zip file
	// (encoded in the byte slice) to permanent storage
	// (e.g. Amazon Glacier) and notes somewhere (a database) that
	// it contains the listed blobs. The blobs are redundant with
	// the filenames in the zip file, which will be named by
	// their blobref string, with no extension.
	Store func(zip []byte, blobs []blob.SizedRef) error

	// DeleteSourceAfterStore, if true, deletes the blobs from Source
	// after Store returns success.
	// This should pretty much always be set true, otherwise subsequent
	// calls to Run/RunOnce will generate the same archives. Wrap
	// the Source in a "namespace" storage if you don't actually
	// want to delete things locally.
	DeleteSourceAfterStore bool
}

// ErrSourceTooSmall is returned by RunOnce if there aren't enough blobs on Source
// to warrant a new zip archive.
var ErrSourceTooSmall = errors.New("archiver: not enough blob data on source to warrant a new zip archive")

func (a *Archiver) zipSize() int64 {
	if a.MinZipSize > 0 {
		return a.MinZipSize
	}
	return DefaultMinZipSize
}

var errStopEnumerate = errors.New("sentinel return value")

// RunOnce scans a.Source and conditionally creates a new zip.
// It returns ErrSourceTooSmall if there aren't enough blobs on Source.
func (a *Archiver) RunOnce() error {
	if a.Source == nil {
		return errors.New("archiver: nil Source")
	}
	if a.Store == nil {
		return errors.New("archiver: nil Store func")
	}
	pz := &potentialZip{a: a}
	err := blobserver.EnumerateAll(context.TODO(), a.Source, func(sb blob.SizedRef) error {
		if err := pz.addBlob(sb); err != nil {
			return err
		}
		if pz.bigEnough() {
			return errStopEnumerate
		}
		return nil
	})
	if err == errStopEnumerate {
		err = nil
	}
	if err != nil {
		return err
	}
	if err := pz.condClose(); err != nil {
		return err
	}
	if !pz.bigEnough() {
		return ErrSourceTooSmall
	}
	if err := a.Store(pz.buf.Bytes(), pz.blobs); err != nil {
		return err
	}
	if a.DeleteSourceAfterStore {
		blobs := make([]blob.Ref, 0, len(pz.blobs))
		for _, sb := range pz.blobs {
			blobs = append(blobs, sb.Ref)
		}
		if err := a.Source.RemoveBlobs(blobs); err != nil {
			return err
		}
	}
	return nil
}

type potentialZip struct {
	a       *Archiver
	blobs   []blob.SizedRef
	zw      *zip.Writer  // nil until actually writing
	buf     bytes.Buffer // of the zip file
	sumSize int64        // of uncompressed bytes of blobs
	closed  bool
}

func (z *potentialZip) bigEnough() bool {
	return int64(z.buf.Len()) > z.a.zipSize()
}

func (z *potentialZip) condClose() error {
	if z.closed || z.zw == nil {
		return nil
	}
	z.closed = true
	return z.zw.Close()
}

func (z *potentialZip) addBlob(sb blob.SizedRef) error {
	if z.bigEnough() {
		return nil
	}
	z.sumSize += int64(sb.Size)
	if z.zw == nil && z.sumSize > z.a.zipSize() {
		z.zw = zip.NewWriter(&z.buf)
		for _, sb := range z.blobs {
			if err := z.writeZipBlob(sb); err != nil {
				return err
			}
		}
	}
	z.blobs = append(z.blobs, sb)
	if z.zw != nil {
		return z.writeZipBlob(sb)
	}
	return nil
}

func (z *potentialZip) writeZipBlob(sb blob.SizedRef) error {
	w, err := z.zw.CreateHeader(&zip.FileHeader{
		Name:   sb.Ref.String(),
		Method: zip.Deflate,
	})
	if err != nil {
		return err
	}
	blobSrc, _, err := z.a.Source.Fetch(sb.Ref)
	if err != nil {
		return err
	}
	defer blobSrc.Close()
	_, err = io.Copy(w, blobSrc)
	return err
}
