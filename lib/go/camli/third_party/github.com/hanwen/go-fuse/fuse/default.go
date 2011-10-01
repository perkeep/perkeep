package fuse

import (
	"os"
)

// DefaultFileSystem
func (me *DefaultFileSystem) GetAttr(name string, context *Context) (*os.FileInfo, Status) {
	return nil, ENOSYS
}

func (me *DefaultFileSystem) GetXAttr(name string, attr string, context *Context) ([]byte, Status) {
	return nil, ENOSYS
}

func (me *DefaultFileSystem) SetXAttr(name string, attr string, data []byte, flags int, context *Context) Status {
	return ENOSYS
}

func (me *DefaultFileSystem) ListXAttr(name string, context *Context) ([]string, Status) {
	return nil, ENOSYS
}

func (me *DefaultFileSystem) RemoveXAttr(name string, attr string, context *Context) Status {
	return ENOSYS
}

func (me *DefaultFileSystem) Readlink(name string, context *Context) (string, Status) {
	return "", ENOSYS
}

func (me *DefaultFileSystem) Mknod(name string, mode uint32, dev uint32, context *Context) Status {
	return ENOSYS
}

func (me *DefaultFileSystem) Mkdir(name string, mode uint32, context *Context) Status {
	return ENOSYS
}

func (me *DefaultFileSystem) Unlink(name string, context *Context) (code Status) {
	return ENOSYS
}

func (me *DefaultFileSystem) Rmdir(name string, context *Context) (code Status) {
	return ENOSYS
}

func (me *DefaultFileSystem) Symlink(value string, linkName string, context *Context) (code Status) {
	return ENOSYS
}

func (me *DefaultFileSystem) Rename(oldName string, newName string, context *Context) (code Status) {
	return ENOSYS
}

func (me *DefaultFileSystem) Link(oldName string, newName string, context *Context) (code Status) {
	return ENOSYS
}

func (me *DefaultFileSystem) Chmod(name string, mode uint32, context *Context) (code Status) {
	return ENOSYS
}

func (me *DefaultFileSystem) Chown(name string, uid uint32, gid uint32, context *Context) (code Status) {
	return ENOSYS
}

func (me *DefaultFileSystem) Truncate(name string, offset uint64, context *Context) (code Status) {
	return ENOSYS
}

func (me *DefaultFileSystem) Open(name string, flags uint32, context *Context) (file File, code Status) {
	return nil, ENOSYS
}

func (me *DefaultFileSystem) OpenDir(name string, context *Context) (stream chan DirEntry, status Status) {
	return nil, ENOSYS
}

func (me *DefaultFileSystem) OnMount(nodeFs *PathNodeFs) {
}

func (me *DefaultFileSystem) OnUnmount() {
}

func (me *DefaultFileSystem) Access(name string, mode uint32, context *Context) (code Status) {
	return ENOSYS
}

func (me *DefaultFileSystem) Create(name string, flags uint32, mode uint32, context *Context) (file File, code Status) {
	return nil, ENOSYS
}

func (me *DefaultFileSystem) Utimens(name string, AtimeNs uint64, CtimeNs uint64, context *Context) (code Status) {
	return ENOSYS
}

func (me *DefaultFileSystem) String() string {
	return "DefaultFileSystem"
}

func (me *DefaultFileSystem) StatFs(name string) *StatfsOut {
	return nil
}
