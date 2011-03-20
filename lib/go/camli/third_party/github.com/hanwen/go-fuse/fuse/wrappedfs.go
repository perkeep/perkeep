package fuse

type WrappingPathFilesystem struct {
	original PathFilesystem
}

func (me *WrappingPathFilesystem) GetAttr(name string) (*Attr, Status) {
	return me.original.GetAttr(name)
}

func (me *WrappingPathFilesystem) Readlink(name string) (string, Status) {
	return me.original.Readlink(name)
}

func (me *WrappingPathFilesystem) Mknod(name string, mode uint32, dev uint32) Status {
	return me.original.Mknod(name, mode, dev)
}

func (me *WrappingPathFilesystem) Mkdir(name string, mode uint32) Status {
	return me.original.Mkdir(name, mode)
}

func (me *WrappingPathFilesystem) Unlink(name string) (code Status) {
	return me.original.Unlink(name)
}

func (me *WrappingPathFilesystem) Rmdir(name string) (code Status) {
	return me.original.Rmdir(name)
}

func (me *WrappingPathFilesystem) Symlink(value string, linkName string) (code Status) {
	return me.original.Symlink(value, linkName)
}

func (me *WrappingPathFilesystem) Rename(oldName string, newName string) (code Status) {
	return me.original.Rename(oldName, newName)
}

func (me *WrappingPathFilesystem) Link(oldName string, newName string) (code Status) {
	return me.original.Link(oldName, newName)
}

func (me *WrappingPathFilesystem) Chmod(name string, mode uint32) (code Status) {
	return me.original.Chmod(name, mode)
}

func (me *WrappingPathFilesystem) Chown(name string, uid uint32, gid uint32) (code Status) {
	return me.original.Chown(name, uid, gid)
}

func (me *WrappingPathFilesystem) Truncate(name string, offset uint64) (code Status) {
	return me.original.Truncate(name, offset)
}

func (me *WrappingPathFilesystem) Open(name string, flags uint32) (file RawFuseFile, code Status) {
	return me.original.Open(name, flags)
}

func (me *WrappingPathFilesystem) OpenDir(name string) (stream chan DirEntry, status Status) {
	return me.original.OpenDir(name)
}

func (me *WrappingPathFilesystem) Mount(conn *PathFileSystemConnector) Status {
	return me.original.Mount(conn)
}

func (me *WrappingPathFilesystem) Unmount() {
	me.original.Unmount()
}

func (me *WrappingPathFilesystem) Access(name string, mode uint32) (code Status) {
	return me.original.Access(name, mode)
}

func (me *WrappingPathFilesystem) Create(name string, flags uint32, mode uint32) (file RawFuseFile, code Status) {
	return me.original.Create(name, flags, mode)
}

func (me *WrappingPathFilesystem) Utimens(name string, AtimeNs uint64, CtimeNs uint64) (code Status) {
	return me.original.Utimens(name, AtimeNs, CtimeNs)
}


////////////////////////////////////////////////////////////////
// Wrapping raw FS.

type WrappingRawFilesystem struct {
	original RawFileSystem
}


func (me *WrappingRawFilesystem) Init(h *InHeader, input *InitIn) (*InitOut, Status) {
	return me.original.Init(h, input)
}

func (me *WrappingRawFilesystem) Destroy(h *InHeader, input *InitIn) {
	me.original.Destroy(h, input)
}

func (me *WrappingRawFilesystem) Lookup(h *InHeader, name string) (out *EntryOut, code Status) {
	return me.original.Lookup(h, name)
}

func (me *WrappingRawFilesystem) Forget(h *InHeader, input *ForgetIn) {
	me.original.Forget(h, input)
}

func (me *WrappingRawFilesystem) GetAttr(header *InHeader, input *GetAttrIn) (out *AttrOut, code Status) {
	return me.original.GetAttr(header, input)
}

func (me *WrappingRawFilesystem) Open(header *InHeader, input *OpenIn) (flags uint32, fuseFile RawFuseFile, status Status) {
	return me.original.Open(header, input)
}

func (me *WrappingRawFilesystem) SetAttr(header *InHeader, input *SetAttrIn) (out *AttrOut, code Status) {
	return me.original.SetAttr(header, input)
}

func (me *WrappingRawFilesystem) Readlink(header *InHeader) (out []byte, code Status) {
	return me.original.Readlink(header)
}

func (me *WrappingRawFilesystem) Mknod(header *InHeader, input *MknodIn, name string) (out *EntryOut, code Status) {
	return me.original.Mknod(header, input, name)
}

func (me *WrappingRawFilesystem) Mkdir(header *InHeader, input *MkdirIn, name string) (out *EntryOut, code Status) {
	return me.original.Mkdir(header, input, name)
}

func (me *WrappingRawFilesystem) Unlink(header *InHeader, name string) (code Status) {
	return me.original.Unlink(header, name)
}

func (me *WrappingRawFilesystem) Rmdir(header *InHeader, name string) (code Status) {
	return me.original.Rmdir(header, name)
}

func (me *WrappingRawFilesystem) Symlink(header *InHeader, pointedTo string, linkName string) (out *EntryOut, code Status) {
	return me.original.Symlink(header, pointedTo, linkName)
}

func (me *WrappingRawFilesystem) Rename(header *InHeader, input *RenameIn, oldName string, newName string) (code Status) {
	return me.original.Rename(header, input, oldName, newName)
}

func (me *WrappingRawFilesystem) Link(header *InHeader, input *LinkIn, name string) (out *EntryOut, code Status) {
	return me.original.Link(header, input, name)
}

func (me *WrappingRawFilesystem) SetXAttr(header *InHeader, input *SetXAttrIn) Status {
	return me.original.SetXAttr(header, input)
}

func (me *WrappingRawFilesystem) GetXAttr(header *InHeader, input *GetXAttrIn) (out *GetXAttrOut, code Status) {
	return me.original.GetXAttr(header, input)
}

func (me *WrappingRawFilesystem) Access(header *InHeader, input *AccessIn) (code Status) {
	return me.original.Access(header, input)
}

func (me *WrappingRawFilesystem) Create(header *InHeader, input *CreateIn, name string) (flags uint32, fuseFile RawFuseFile, out *EntryOut, code Status) {
	return me.original.Create(header, input, name)
}

func (me *WrappingRawFilesystem) Bmap(header *InHeader, input *BmapIn) (out *BmapOut, code Status) {
	return me.original.Bmap(header, input)
}

func (me *WrappingRawFilesystem) Ioctl(header *InHeader, input *IoctlIn) (out *IoctlOut, code Status) {
	return me.original.Ioctl(header, input)
}

func (me *WrappingRawFilesystem) Poll(header *InHeader, input *PollIn) (out *PollOut, code Status) {
	return me.original.Poll(header, input)
}

func (me *WrappingRawFilesystem) OpenDir(header *InHeader, input *OpenIn) (flags uint32, fuseFile RawFuseDir, status Status) {
	return me.original.OpenDir(header, input)
}

func (me *WrappingRawFilesystem) Release(header *InHeader, f RawFuseFile) {
	me.original.Release(header, f)
}

func (me *WrappingRawFilesystem) ReleaseDir(header *InHeader, f RawFuseDir) {
	me.original.ReleaseDir(header, f)
}



