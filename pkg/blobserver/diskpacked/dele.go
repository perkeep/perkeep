/*
Copyright 2013 The Camlistore Authors

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

package diskpacked

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"

	"camlistore.org/pkg/blob"
)

var errNoPunch = errors.New("punchHole not supported")

// punchHole, if non-nil, punches a hole in f from offset to offset+size.
var punchHole func(file *os.File, offset int64, size int64) error

func (s *storage) delete(br blob.Ref) error {
	meta, err := s.meta(br)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.filename(meta.file), os.O_RDWR, 0666)
	if err != nil {
		return err
	}
	defer f.Close()

	// walk back, find the header, and overwrite the hash with xxxx-000000...
	k := 1 + len(br.String()) + 1 + len(strconv.FormatUint(uint64(meta.size), 10)) + 1
	off := meta.offset - int64(k)
	b := make([]byte, k)
	if k, err = f.ReadAt(b, off); err != nil {
		return err
	}
	if b[0] != byte('[') || b[k-1] != byte(']') {
		return fmt.Errorf("delete: cannot find header surroundings, found %q", b)
	}
	b = b[1 : k-1] // "sha1-xxxxxxxxxxxxxxxxxx nnnn" - everything between []
	off += 1

	// Replace b with "xxxx-000000000"
	dash := bytes.IndexByte(b, '-')
	if dash < 0 {
		return fmt.Errorf("delete: cannot find dash in ref %q", b)
	}
	space := bytes.IndexByte(b[dash+1:], ' ')
	if space < 0 {
		return fmt.Errorf("delete: cannot find space in header %q", b)
	}
	for i := 0; i < dash; i++ {
		b[i] = 'x'
	}
	for i := dash + 1; i < dash+1+space; i++ {
		b[i] = '0'
	}

	// write back
	if _, err = f.WriteAt(b, off); err != nil {
		return err
	}

	// punch hole, if possible
	if punchHole != nil {
		err = punchHole(f, meta.offset, int64(meta.size))
		if err == nil {
			return nil
		}
		if err != errNoPunch {
			return err
		}
		// err == errNoPunch - not implemented
	}

	// fill with zero
	n, err := f.Seek(meta.offset, os.SEEK_SET)
	if err != nil {
		return err
	}
	if n != meta.offset {
		return fmt.Errorf("error seeking to %d: got %d", meta.offset, n)
	}
	_, err = io.CopyN(f, zeroReader{}, int64(meta.size))
	return err
}

type zeroReader struct{}

func (z zeroReader) Read(p []byte) (n int, err error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}
