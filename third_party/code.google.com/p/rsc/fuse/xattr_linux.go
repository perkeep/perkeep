// Copyright 2013 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fuse

import "syscall"

// This is mildly confusing, but the value to return when an attribute
// doesn't exist seems to vary from system to system.
// http://mail-index.netbsd.org/tech-kern/2012/04/30/msg013091.html

// ENOATTR is a fuse.Error returned when a request is made for an
// extended attribute that doesn't exist.
var ENOATTR = Errno(syscall.ENODATA)
