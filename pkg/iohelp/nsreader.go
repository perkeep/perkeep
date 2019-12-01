package iohelp

import "io"

// NamedSectionReader is an io.SectionReader built on an underlying NamedReadAtCloser
// that also implements Namer and Section.
type NamedSectionReader struct {
	*io.SectionReader
	r      NamedReadAtCloser
	offset int64
}

// NewNamedSectionReader creates a new NamedSectionReader.
func NewNamedSectionReader(r NamedReadAtCloser, off, n int64) *NamedSectionReader {
	return &NamedSectionReader{
		SectionReader: io.NewSectionReader(r, off, n),
		r:             r,
		offset:        off,
	}
}

func (fsr *NamedSectionReader) Name() string {
	return fsr.r.Name()
}

func (fsr *NamedSectionReader) Offset() int64 {
	return fsr.offset
}

func (fsr *NamedSectionReader) Close() error {
	return fsr.r.Close()
}
