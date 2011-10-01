package fuse

// all of the code for DirEntryList.

import (
	"bytes"
	"fmt"
	"unsafe"
)

var _ = fmt.Print

// For FileSystemConnector.  The connector determines inodes.
type DirEntry struct {
	Mode uint32
	Name string
}

type DirEntryList struct {
	buf     bytes.Buffer
	offset  *uint64
	maxSize int
}

func NewDirEntryList(max int, off *uint64) *DirEntryList {
	return &DirEntryList{maxSize: max, offset: off}
}

func (me *DirEntryList) AddString(name string, inode uint64, mode uint32) bool {
	return me.Add([]byte(name), inode, mode)
}

func (me *DirEntryList) AddDirEntry(e DirEntry) bool {
	return me.Add([]byte(e.Name), uint64(FUSE_UNKNOWN_INO), e.Mode)
}

func (me *DirEntryList) Add(name []byte, inode uint64, mode uint32) bool {
	lastLen := me.buf.Len()
	(*me.offset)++

	dirent := Dirent{
		Off:     *me.offset,
		Ino:     inode,
		NameLen: uint32(len(name)),
		Typ:     ModeToType(mode),
	}

	_, err := me.buf.Write(asSlice(unsafe.Pointer(&dirent), unsafe.Sizeof(Dirent{})))
	if err != nil {
		panic("Serialization of Dirent failed")
	}
	me.buf.Write(name)

	padding := 8 - len(name)&7
	if padding < 8 {
		me.buf.Write(make([]byte, padding))
	}

	if me.buf.Len() > me.maxSize {
		me.buf.Truncate(lastLen)
		(*me.offset)--
		return false
	}
	return true
}

func (me *DirEntryList) Bytes() []byte {
	return me.buf.Bytes()
}

////////////////////////////////////////////////////////////////

type rawDir interface {
	ReadDir(input *ReadIn) (*DirEntryList, Status)
	Release()
}

type connectorDir struct {
	extra      []DirEntry
	stream     chan DirEntry
	leftOver   DirEntry
	lastOffset uint64
}

func (me *connectorDir) ReadDir(input *ReadIn) (*DirEntryList, Status) {
	if me.stream == nil && len(me.extra) == 0 {
		return nil, OK
	}

	list := NewDirEntryList(int(input.Size), &me.lastOffset)
	if me.leftOver.Name != "" {
		success := list.AddDirEntry(me.leftOver)
		if !success {
			panic("No space for single entry.")
		}
		me.leftOver.Name = ""
	}
	for len(me.extra) > 0 {
		e := me.extra[len(me.extra)-1]
		me.extra = me.extra[:len(me.extra)-1]
		success := list.AddDirEntry(e)
		if !success {
			me.leftOver = e
			return list, OK
		}
	}
	for {
		d, isOpen := <-me.stream
		if !isOpen {
			me.stream = nil
			break
		}
		if !list.AddDirEntry(d) {
			me.leftOver = d
			break
		}
	}
	return list, OK
}

// Read everything so we make goroutines exit.
func (me *connectorDir) Release() {
	for ok := true; ok && me.stream != nil; {
		_, ok = <-me.stream
		if !ok {
			break
		}
	}
}
