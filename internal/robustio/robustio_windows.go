package robustio

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

func isEphemeralError(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case syscall.ERROR_ACCESS_DENIED,
			syscall.ERROR_FILE_NOT_FOUND,
			windows.ERROR_SHARING_VIOLATION:
			return true
		}
	}
	return false
}
