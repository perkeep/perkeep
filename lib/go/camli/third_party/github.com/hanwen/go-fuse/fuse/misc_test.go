package fuse

import (
	"os"
	"testing"
	"syscall"
)


func TestOsErrorToFuseError(t *testing.T) {
	errNo := OsErrorToFuseError(os.EPERM)
	if errNo != syscall.EPERM {
		t.Errorf("Wrong conversion %v != %v", errNo, syscall.EPERM)
	}

	e := os.NewSyscallError("syscall", syscall.EPERM)
	errNo = OsErrorToFuseError(e)
	if errNo != syscall.EPERM {
		t.Errorf("Wrong conversion %v != %v", errNo, syscall.EPERM)
	}

	e = os.Remove("this-file-surely-does-not-exist")
	errNo = OsErrorToFuseError(e)
	if errNo != syscall.ENOENT {
		t.Errorf("Wrong conversion %v != %v", errNo, syscall.ENOENT)
	}
}
