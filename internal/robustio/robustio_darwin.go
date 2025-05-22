package robustio

import (
	"errors"
	"syscall"
)

func isEphemeralError(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == syscall.ENOENT
	}
	return false
}
