package iohelp

// Section is an interface for io.SectionReader-style wrapper types
// that can report the offset and size of the underlying reader's section.
// Note that io.SectionReader does _not_ implement this interface.
// (It's missing the Offset method.)
// If a Section that is also a Namer is presented to fsbacked.Storage.ReceiveBlob,
// then the designated section of the existing file is used as storage for that blob.
type Section interface {
	// Offset reports the offset of this section relative to the underlying source.
	// If the offset isn't known, this returns -1.
	Offset() int64

	// Size reports the size of this section.
	// If the size isn't known, this returns -1.
	Size() int64
}
