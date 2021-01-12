package iohelp

import (
	"io"
)

// Reader interfaces for detecting when a file or part of a file is being uploaded.

// Namer is the type of an object that can report its name.
// It is implemented by os.File.
// It should be implemented by any os.File-wrapping reader type
// that wishes the caller to know the name of the underlying file.
// In particular, the io.Reader presented to fsbacked.Storage.ReceiveBlob
// should be a Namer in order to get fsbacked functionality.
type Namer interface {
	// Name reports the name of the object.
	// If the name isn't known, Name returns "".
	Name() string
}

// NamedReadAtCloser is a Namer that is also an io.ReaderAt and an io.Closer.
// It is implemented by os.File.
type NamedReadAtCloser interface {
	io.ReaderAt
	io.Closer
	Namer
}
