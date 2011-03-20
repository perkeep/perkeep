package fuse

// A FUSE file for read-only filesystems.  This assumes we already have the data in memory.

type ReadOnlyFile struct {
	data []byte

	DefaultRawFuseFile
}

func NewReadOnlyFile(data []byte) *ReadOnlyFile {
	f := new(ReadOnlyFile)
	f.data = data
	return f
}

func (me *ReadOnlyFile) Read(input *ReadIn, bp *BufferPool) ([]byte, Status) {
	end := int(input.Offset) + int(input.Size)
	if end > len(me.data) {
		end = len(me.data)
	}

	return me.data[input.Offset:end], OK
}
