//go:build windows || darwin

package robustio

import (
	"errors"
	"math/rand"
	"os"
	"syscall"
	"time"
)

func rename(oldname, newname string) error {
	return retry(func() (err error, mayRetry bool) {
		err = os.Rename(oldname, newname)
		return err, isEphemeralError(err)
	})
}

func remove(path string) error {
	return retry(func() (err error, mayRetry bool) {
		err = os.Remove(path)
		return err, isEphemeralError(err)
	})
}

const arbitraryTimeout = 2000 * time.Millisecond

func retry(f func() (err error, mayRetry bool)) error {
	var (
		bestErr     error
		lowestErrno syscall.Errno
		start       time.Time
		nextSleep   time.Duration = 1 * time.Millisecond
	)
	for {
		err, mayRetry := f()
		if err == nil || !mayRetry {
			return err
		}

		var errno syscall.Errno
		if errors.As(err, &errno) && (lowestErrno == 0 || errno < lowestErrno) {
			bestErr = err
			lowestErrno = errno
		} else if bestErr == nil {
			bestErr = err
		}

		if start.IsZero() {
			start = time.Now()
		} else if d := time.Since(start) + nextSleep; d >= arbitraryTimeout {
			break
		}
		time.Sleep(nextSleep)
		nextSleep += time.Duration(rand.Int63n(int64(nextSleep)))
	}

	return bestErr
}
