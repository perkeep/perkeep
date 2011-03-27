package fuse

import (
	"log"
)

var _ = log.Println

func (me *DefaultRawFuseFileSystem) Init(h *InHeader, input *InitIn) (*InitOut, Status) {
	return new(InitOut), OK
}

func (me *DefaultRawFuseFileSystem) Destroy(h *InHeader, input *InitIn) {

}

func (me *DefaultRawFuseFileSystem) Lookup(h *InHeader, name string) (out *EntryOut, code Status) {
	return nil, ENOSYS
}

func (me *DefaultRawFuseFileSystem) Forget(h *InHeader, input *ForgetIn) {
}

func (me *DefaultRawFuseFileSystem) GetAttr(header *InHeader, input *GetAttrIn) (out *AttrOut, code Status) {
	return nil, ENOSYS
}

func (me *DefaultRawFuseFileSystem) Open(header *InHeader, input *OpenIn) (flags uint32, fuseFile RawFuseFile, status Status) {
	return 0, nil, OK
}

func (me *DefaultRawFuseFileSystem) SetAttr(header *InHeader, input *SetAttrIn) (out *AttrOut, code Status) {
	return nil, ENOSYS
}

func (me *DefaultRawFuseFileSystem) Readlink(header *InHeader) (out []byte, code Status) {
	return nil, ENOSYS
}

func (me *DefaultRawFuseFileSystem) Mknod(header *InHeader, input *MknodIn, name string) (out *EntryOut, code Status) {
	return new(EntryOut), ENOSYS
}

func (me *DefaultRawFuseFileSystem) Mkdir(header *InHeader, input *MkdirIn, name string) (out *EntryOut, code Status) {
	return nil, ENOSYS
}

func (me *DefaultRawFuseFileSystem) Unlink(header *InHeader, name string) (code Status) {
	return ENOSYS
}

func (me *DefaultRawFuseFileSystem) Rmdir(header *InHeader, name string) (code Status) {
	return ENOSYS
}

func (me *DefaultRawFuseFileSystem) Symlink(header *InHeader, pointedTo string, linkName string) (out *EntryOut, code Status) {
	return nil, ENOSYS
}

func (me *DefaultRawFuseFileSystem) Rename(header *InHeader, input *RenameIn, oldName string, newName string) (code Status) {
	return ENOSYS
}

func (me *DefaultRawFuseFileSystem) Link(header *InHeader, input *LinkIn, name string) (out *EntryOut, code Status) {
	return nil, ENOSYS
}

func (me *DefaultRawFuseFileSystem) SetXAttr(header *InHeader, input *SetXAttrIn) Status {
	return ENOSYS
}

func (me *DefaultRawFuseFileSystem) GetXAttr(header *InHeader, attr string) (data []byte, code Status) {
	return nil, ENOSYS
}

func (me *DefaultRawFuseFileSystem) Access(header *InHeader, input *AccessIn) (code Status) {
	return ENOSYS
}

func (me *DefaultRawFuseFileSystem) Create(header *InHeader, input *CreateIn, name string) (flags uint32, fuseFile RawFuseFile, out *EntryOut, code Status) {
	return 0, nil, nil, ENOSYS
}

func (me *DefaultRawFuseFileSystem) Bmap(header *InHeader, input *BmapIn) (out *BmapOut, code Status) {
	return nil, ENOSYS
}

func (me *DefaultRawFuseFileSystem) Ioctl(header *InHeader, input *IoctlIn) (out *IoctlOut, code Status) {
	return nil, ENOSYS
}

func (me *DefaultRawFuseFileSystem) Poll(header *InHeader, input *PollIn) (out *PollOut, code Status) {
	return nil, ENOSYS
}

func (me *DefaultRawFuseFileSystem) OpenDir(header *InHeader, input *OpenIn) (flags uint32, fuseFile RawFuseDir, status Status) {
	return 0, nil, ENOSYS
}

func (me *DefaultRawFuseFileSystem) Release(header *InHeader, f RawFuseFile) {
}

func (me *DefaultRawFuseFileSystem) ReleaseDir(header *InHeader, f RawFuseDir) {
}


////////////////////////////////////////////////////////////////
//  DefaultRawFuseFile

func (me *DefaultRawFuseFile) Read(*ReadIn, *BufferPool) ([]byte, Status) {
	return []byte(""), ENOSYS
}

func (me *DefaultRawFuseFile) Write(*WriteIn, []byte) (uint32, Status) {
	return 0, ENOSYS
}

func (me *DefaultRawFuseFile) Flush() Status {
	return ENOSYS
}

func (me *DefaultRawFuseFile) Release() {

}

func (me *DefaultRawFuseFile) Fsync(*FsyncIn) (code Status) {
	return ENOSYS
}


////////////////////////////////////////////////////////////////
//

func (me *DefaultRawFuseDir) ReadDir(input *ReadIn) (*DirEntryList, Status) {
	return nil, ENOSYS
}

func (me *DefaultRawFuseDir) ReleaseDir() {
}

func (me *DefaultRawFuseDir) FsyncDir(input *FsyncIn) (code Status) {
	return ENOSYS
}

////////////////////////////////////////////////////////////////
// DefaultPathFilesystem

func (me *DefaultPathFilesystem) GetAttr(name string) (*Attr, Status) {
	return nil, ENOSYS
}

func (me *DefaultPathFilesystem) GetXAttr(name string, attr string) ([]byte, Status) {
	return nil, ENOSYS
}

func (me *DefaultPathFilesystem) Readlink(name string) (string, Status) {
	return "", ENOSYS
}

func (me *DefaultPathFilesystem) Mknod(name string, mode uint32, dev uint32) Status {
	return ENOSYS
}

func (me *DefaultPathFilesystem) Mkdir(name string, mode uint32) Status {
	return ENOSYS
}

func (me *DefaultPathFilesystem) Unlink(name string) (code Status) {
	return ENOSYS
}

func (me *DefaultPathFilesystem) Rmdir(name string) (code Status) {
	return ENOSYS
}

func (me *DefaultPathFilesystem) Symlink(value string, linkName string) (code Status) {
	return ENOSYS
}

func (me *DefaultPathFilesystem) Rename(oldName string, newName string) (code Status) {
	return ENOSYS
}

func (me *DefaultPathFilesystem) Link(oldName string, newName string) (code Status) {
	return ENOSYS
}

func (me *DefaultPathFilesystem) Chmod(name string, mode uint32) (code Status) {
	return ENOSYS
}

func (me *DefaultPathFilesystem) Chown(name string, uid uint32, gid uint32) (code Status) {
	return ENOSYS
}

func (me *DefaultPathFilesystem) Truncate(name string, offset uint64) (code Status) {
	return ENOSYS
}

func (me *DefaultPathFilesystem) Open(name string, flags uint32) (file RawFuseFile, code Status) {
	return nil, ENOSYS
}

func (me *DefaultPathFilesystem) OpenDir(name string) (stream chan DirEntry, status Status) {
	return nil, ENOSYS
}

func (me *DefaultPathFilesystem) Mount(conn *PathFileSystemConnector) Status {
	return OK
}

func (me *DefaultPathFilesystem) Unmount() {
}

func (me *DefaultPathFilesystem) Access(name string, mode uint32) (code Status) {
	return ENOSYS
}

func (me *DefaultPathFilesystem) Create(name string, flags uint32, mode uint32) (file RawFuseFile, code Status) {
	return nil, ENOSYS
}

func (me *DefaultPathFilesystem) Utimens(name string, AtimeNs uint64, CtimeNs uint64) (code Status) {
	return ENOSYS
}
