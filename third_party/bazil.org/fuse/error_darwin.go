package fuse

import (
	"syscall"
)

// getxattr return value for "extended attribute does not exist" is
// ENOATTR on OS X, and ENODATA on Linux and apparently at least
// NetBSD. There may be a #define ENOATTR too, but the value is
// ENODATA in the actual syscalls. ENOATTR is not in any of the
// standards, ENODATA exists but is only used for STREAMs.
//
// https://developer.apple.com/library/mac/documentation/Darwin/Reference/ManPages/man2/getxattr.2.html
// http://mail-index.netbsd.org/tech-kern/2012/04/30/msg013090.html
// http://mail-index.netbsd.org/tech-kern/2012/04/30/msg013097.html
// http://pubs.opengroup.org/onlinepubs/9699919799/basedefs/errno.h.html
func translateGetxattrError(err Error) Error {
	switch err {
	case ENODATA:
		return Errno(syscall.ENOATTR)
	}
	return err
}
