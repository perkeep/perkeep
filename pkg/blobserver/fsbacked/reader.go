package fsbacked

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
	Name() string
}

// NamedReadAtCloser is a Namer that is also an io.ReaderAt and an io.Closer.
// It is implemented by os.File.
type NamedReadAtCloser interface {
	io.ReaderAt
	io.Closer
	Namer
}

// Section is an interface for io.SectionReader-style wrapper types
// that can report the offset and size of the underlying reader's section.
// Note that io.SectionReader does _not_ implement this interface.
// (It's missing the Offset method.)
// If a Section that is also a Namer is presented to fsbacked.Storage.ReceiveBlob,
// then the designated section of the existing file is used as storage for that blob.
type Section interface {
	Offset() int64
	Size() int64
}

// FileSectionReader is an io.SectionReader built on an underlying NamedReadAtCloser
// that also implements Section.
type FileSectionReader struct {
	*io.SectionReader
	r      NamedReadAtCloser
	offset int64
}

// NewFileSectionReader creates a new FileSectionReader.
func NewFileSectionReader(r NamedReadAtCloser, off, n int64) *FileSectionReader {
	return &FileSectionReader{
		SectionReader: io.NewSectionReader(r, off, n),
		r:             r,
		offset:        off,
	}
}

func (fsr *FileSectionReader) Name() string {
	return fsr.r.Name()
}

func (fsr *FileSectionReader) Offset() int64 {
	return fsr.offset
}

func (fsr *FileSectionReader) Close() error {
	return fsr.r.Close()
}
