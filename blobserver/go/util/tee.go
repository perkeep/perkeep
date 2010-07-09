package util

import (
	"io"
	"os"
)

type teeWriter struct {
	writers []io.Writer
}

// Writes to all writers in the tee.  Note that the return value
// doesn't actually capture the failure state well, unable to convey
// where the error occurred and which writers got how much data.
// But on success, written == len(p) and err is nil.
func (t *teeWriter) Write(p []byte) (written int, err os.Error) {
	for _, w := range t.writers {
		nw, ew := w.Write(p)
		if ew != nil {
			err = ew
			return
		}
		if nw != len(p) {
			written = nw
			err = io.ErrShortWrite
			return
		}
	}
	return len(p), nil
}

func NewTee(writers ...io.Writer) io.Writer {
	return &teeWriter{writers}
}
