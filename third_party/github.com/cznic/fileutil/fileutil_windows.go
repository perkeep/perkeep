// Copyright (c) 2013 jnml. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fileutil

import (
	"io"
	"os"
	"syscall"
)

// PunchHole deallocates space inside a file in the byte range starting at
// offset and continuing for len bytes. Not supported on Windows.
func PunchHole(f *os.File, off, len int64) error {
	return nil
}

// Fadvise predeclares an access pattern for file data.  See also 'man 2
// posix_fadvise'. Not supported on Windows.
func Fadvise(f *os.File, off, len int64, advice FadviseAdvice) error {
	return nil
}

// IsEOF reports whether err is an EOF condition.
func IsEOF(err error) bool {
	if err == io.EOF {
		return true
	}

	// http://social.technet.microsoft.com/Forums/windowsserver/en-US/1a16311b-c625-46cf-830b-6a26af488435/how-to-solve-error-38-0x26-errorhandleeof-using-fsctlgetretrievalpointers
	x, ok := err.(*os.PathError)
	return ok && x.Op == "read" && x.Err.(syscall.Errno) == 0x26
}
