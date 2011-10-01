package fuse

import (
	"os"
	"testing"
	"syscall"
)

func TestOsErrorToErrno(t *testing.T) {
	errNo := OsErrorToErrno(os.EPERM)
	if errNo != syscall.EPERM {
		t.Errorf("Wrong conversion %v != %v", errNo, syscall.EPERM)
	}

	e := os.NewSyscallError("syscall", syscall.EPERM)
	errNo = OsErrorToErrno(e)
	if errNo != syscall.EPERM {
		t.Errorf("Wrong conversion %v != %v", errNo, syscall.EPERM)
	}

	e = os.Remove("this-file-surely-does-not-exist")
	errNo = OsErrorToErrno(e)
	if errNo != syscall.ENOENT {
		t.Errorf("Wrong conversion %v != %v", errNo, syscall.ENOENT)
	}
}
