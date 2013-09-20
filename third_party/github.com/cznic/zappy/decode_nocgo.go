// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Copyright 2011 The Snappy-Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the SNAPPY-GO-LICENSE file.

package zappy

import (
	"encoding/binary"
)

// Decode returns the decoded form of src. The returned slice may be a sub-
// slice of dst if dst was large enough to hold the entire decoded block.
// Otherwise, a newly allocated slice will be returned.
// It is valid to pass a nil dst.
func Decode(dst, src []byte) ([]byte, error) {
	dLen, s, err := decodedLen(src)
	if err != nil {
		return nil, err
	}

	if dLen == 0 {
		if len(src) == 1 {
			return nil, nil
		}

		return nil, ErrCorrupt
	}

	if len(dst) < dLen {
		dst = make([]byte, dLen)
	}

	var d, offset, length int
	for s < len(src) {
		n, i := binary.Varint(src[s:])
		if i <= 0 {
			return nil, ErrCorrupt
		}

		s += i
		if n >= 0 {
			length = int(n + 1)
			if length > len(dst)-d || length > len(src)-s {
				return nil, ErrCorrupt
			}

			copy(dst[d:], src[s:s+length])
			d += length
			s += length
			continue
		}

		length = int(-n)
		off64, i := binary.Uvarint(src[s:])
		if i <= 0 {
			return nil, ErrCorrupt
		}

		offset = int(off64)
		s += i
		if s > len(src) {
			return nil, ErrCorrupt
		}

		end := d + length
		if offset > d || end > len(dst) {
			return nil, ErrCorrupt
		}

		for s, v := range dst[d-offset : end-offset] {
			dst[d+s] = v
		}
		d = end

	}
	if d != dLen {
		return nil, ErrCorrupt
	}

	return dst[:d], nil
}
