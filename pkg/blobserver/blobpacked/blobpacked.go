/*
Copyright 2014 The Camlistore AUTHORS

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

/* Package blobpacked registers the "blobpacked" blobserver storage
type, storing blobs initially as one physical blob per logical blob,
but then rearranging little physical blobs into large contiguous blobs
organized by how they'll likely be accessed. An index tracks the
mapping from logical to physical blobs.

Example low-level config:

     "/storage/": {
         "handler": "storage-blobpacked",
         "handlerArgs": {
            "smallBlobs": "/small/",
            "largeBlobs": "/large/",
            "metaIndex": {
               "type": "mysql",
                .....
            }
          }
     }

*/
package blobpacked

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/context"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/strutil"
)

type storage struct {
	small blobserver.Storage
	large blobserver.Storage

	// meta key -> value rows are:
	//   sha1-xxxx -> "<size> s"
	//   sha1-xxxx -> "<size> l <big-blobref> <big-offset>"
	meta sorted.KeyValue
}

func (s *storage) String() string {
	return fmt.Sprintf("\"blobpacked\" storage")
}

func newFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (blobserver.Storage, error) {
	var (
		smallPrefix = conf.RequiredString("smallBlobs")
		largePrefix = conf.RequiredString("largeBlobs")
		metaConf    = conf.RequiredObject("metaIndex")
	)
	if err := conf.Validate(); err != nil {
		return nil, err
	}
	small, err := ld.GetStorage(smallPrefix)
	if err != nil {
		return nil, fmt.Errorf("failed to load smallBlobs at %s: %v", smallPrefix, err)
	}
	large, err := ld.GetStorage(largePrefix)
	if err != nil {
		return nil, fmt.Errorf("failed to load largeBlobs at %s: %v", largePrefix, err)
	}
	meta, err := sorted.NewKeyValue(metaConf)
	if err != nil {
		return nil, fmt.Errorf("failed to setup blobpacked metaIndex: %v", err)
	}
	sto := &storage{
		small: small,
		large: large,
		meta:  meta,
	}
	return sto, nil
}

func init() {
	blobserver.RegisterStorageConstructor("blobpacked", blobserver.StorageConstructor(newFromConfig))
}

func (s *storage) Close() error {
	return nil
}

type meta struct {
	exists   bool
	size     uint32
	largeRef blob.Ref // if invalid, then on small if exists
	largeOff uint32
}

// if not found, err == nil.
func (s *storage) getMetaRow(br blob.Ref) (meta, error) {
	v, err := s.meta.Get(br.String())
	if err == sorted.ErrNotFound {
		return meta{}, nil
	}
	return parseMetaRow([]byte(v))
}

var singleSpace = []byte{' '}

// parses one of:
// "<size> s"
// "<size> l <big-blobref> <big-offset>"
func parseMetaRow(v []byte) (m meta, err error) {
	row := v
	sp := bytes.IndexByte(v, ' ')
	if sp < 1 || sp == len(v)-1 {
		return meta{}, fmt.Errorf("invalid metarow %q", v)
	}
	m.exists = true
	size, err := strutil.ParseUintBytes(v[:sp], 10, 32)
	if err != nil {
		return meta{}, fmt.Errorf("invalid metarow size %q", v)
	}
	m.size = uint32(size)
	v = v[sp+1:]
	switch v[0] {
	default:
		return meta{}, fmt.Errorf("invalid metarow type %q", v)
	case 's':
		if len(v) > 1 {
			return meta{}, fmt.Errorf("invalid small metarow %q", v)
		}
		return
	case 'l':
		if len(v) < 2 || v[1] != ' ' {
			err = errors.New("length")
			break
		}
		v = v[2:] // remains: "<big-blobref> <big-offset>"
		if bytes.Count(v, singleSpace) != 1 {
			err = errors.New("number of spaces")
			break
		}
		sp := bytes.IndexByte(v, ' ')
		largeRef, ok := blob.ParseBytes(v[:sp])
		if !ok {
			err = fmt.Errorf("bad blobref %q", v[:sp])
			break
		}
		m.largeRef = largeRef
		off, err := strutil.ParseUintBytes(v[sp+1:], 10, 32)
		if err != nil {
			break
		}
		m.largeOff = uint32(off)
		return m, nil
	}
	return meta{}, fmt.Errorf("invalid metarow %q: %v", row, err)
}

func (s *storage) ReceiveBlob(br blob.Ref, source io.Reader) (sb blob.SizedRef, err error) {
	var buf bytes.Buffer // TODO: reuse?
	if _, err := io.Copy(&buf, source); err != nil {
		return sb, err
	}
	size := uint32(buf.Len())
	isFile := false
	if b, err := schema.BlobFromReader(br, bytes.NewReader(buf.Bytes())); err == nil && b.Type() == "file" {
		isFile = true
	}
	meta, err := s.getMetaRow(br)
	if err != nil {
		return sb, err
	}
	if meta.exists {
		if isFile {
			// TODO try to reconstruct
		}
		return blob.SizedRef{Size: size, Ref: br}, nil
	}
	sb, err = s.small.ReceiveBlob(br, &buf)
	if err != nil {
		return
	}
	if isFile {
		// TODO try to reconstruct
	}
	if err := s.meta.Set(br.String(), fmt.Sprintf("%d s", size)); err != nil {
		return sb, err
	}
	return sb, nil
}

func (s *storage) Fetch(br blob.Ref) (io.ReadCloser, uint32, error) {
	m, err := s.getMetaRow(br)
	if err != nil {
		return nil, 0, err
	}
	if !m.exists {
		return nil, 0, os.ErrNotExist
	}
	if m.largeRef.Valid() {
		return s.large.Fetch(br)
	} else {
		return s.small.Fetch(br)
	}
}

func (s *storage) RemoveBlobs(blobs []blob.Ref) error {
	// TODO: how to support? only delete from index? delete from small if only there?
	// if in big file, re-break apart into its chunks? no reverse index, though.
	return errors.New("not implemented")
}

func (s *storage) StatBlobs(dest chan<- blob.SizedRef, blobs []blob.Ref) (err error) {
	for _, br := range blobs {
		m, err := s.getMetaRow(br)
		if err != nil {
			return err
		}
		if m.exists {
			dest <- blob.SizedRef{Ref: br, Size: m.size}
		}
	}
	return nil
}

func (s *storage) EnumerateBlobs(ctx *context.Context, dest chan<- blob.SizedRef, after string, limit int) (err error) {
	defer close(dest)
	t := s.meta.Find(after, "")
	defer func() {
		closeErr := t.Close()
		if err == nil {
			err = closeErr
		}
	}()
	n := 0
	for n < limit && t.Next() {
		if n == 0 && t.Key() == after {
			continue
		}
		n++
		key, val := t.KeyBytes(), t.ValueBytes()
		br, ok := blob.ParseBytes(key)
		if !ok {
			return fmt.Errorf("unknown key %q in meta index", key)
		}
		m, err := parseMetaRow(val)
		if err != nil {
			return err
		}
		if !m.exists {
			panic("should exist if here")
		}
		dest <- blob.SizedRef{Ref: br, Size: m.size}
	}
	return nil
}
