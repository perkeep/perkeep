// Copyright 2013 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fuse

import "syscall"

// ENOATTR is a fuse.Error returned when a request is made for an
// extended attribute that doesn't exist.
var ENOATTR = Errno(syscall.ENOATTR)
