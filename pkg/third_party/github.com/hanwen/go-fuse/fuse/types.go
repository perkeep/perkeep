package fuse

import (
	"syscall"
)

const (
	FUSE_KERNEL_VERSION = 7

	FUSE_KERNEL_MINOR_VERSION = 13

	FUSE_ROOT_ID = 1

	// SetAttrIn.Valid
	FATTR_MODE      = (1 << 0)
	FATTR_UID       = (1 << 1)
	FATTR_GID       = (1 << 2)
	FATTR_SIZE      = (1 << 3)
	FATTR_ATIME     = (1 << 4)
	FATTR_MTIME     = (1 << 5)
	FATTR_FH        = (1 << 6)
	FATTR_ATIME_NOW = (1 << 7)
	FATTR_MTIME_NOW = (1 << 8)
	FATTR_LOCKOWNER = (1 << 9)

	// OpenIn.Flags
	FOPEN_DIRECT_IO   = (1 << 0)
	FOPEN_KEEP_CACHE  = (1 << 1)
	FOPEN_NONSEEKABLE = (1 << 2)

	// To be set in InitOut.Flags.
	FUSE_ASYNC_READ     = (1 << 0)
	FUSE_POSIX_LOCKS    = (1 << 1)
	FUSE_FILE_OPS       = (1 << 2)
	FUSE_ATOMIC_O_TRUNC = (1 << 3)
	FUSE_EXPORT_SUPPORT = (1 << 4)
	FUSE_BIG_WRITES     = (1 << 5)
	FUSE_DONT_MASK      = (1 << 6)

	FUSE_UNKNOWN_INO = 0xffffffff

	CUSE_UNRESTRICTED_IOCTL = (1 << 0)

	FUSE_RELEASE_FLUSH = (1 << 0)

	FUSE_GETATTR_FH = (1 << 0)

	FUSE_LK_FLOCK = (1 << 0)

	FUSE_WRITE_CACHE     = (1 << 0)
	FUSE_WRITE_LOCKOWNER = (1 << 1)

	FUSE_READ_LOCKOWNER = (1 << 1)

	FUSE_IOCTL_COMPAT       = (1 << 0)
	FUSE_IOCTL_UNRESTRICTED = (1 << 1)
	FUSE_IOCTL_RETRY        = (1 << 2)

	FUSE_IOCTL_MAX_IOV = 256

	FUSE_POLL_SCHEDULE_NOTIFY = (1 << 0)


	FUSE_COMPAT_WRITE_IN_SIZE = 24

	FUSE_MIN_READ_BUFFER = 8192

	FUSE_COMPAT_ENTRY_OUT_SIZE = 120

	FUSE_COMPAT_ATTR_OUT_SIZE = 96

	FUSE_COMPAT_MKNOD_IN_SIZE = 8

	FUSE_COMPAT_STATFS_SIZE = 48

	CUSE_INIT_INFO_MAX = 4096

	S_IFDIR = syscall.S_IFDIR
	S_IFREG = syscall.S_IFREG

	// TODO - get this from a canonical place.
	PAGESIZE = 4096
)

type Status int32

const (
	OK      = Status(0)
	EACCES  = Status(syscall.EACCES)
	EBUSY   = Status(syscall.EBUSY)
	EINVAL  = Status(syscall.EINVAL)
	EIO     = Status(syscall.EIO)
	ENOENT  = Status(syscall.ENOENT)
	ENOSYS  = Status(syscall.ENOSYS)
	ENOTDIR = Status(syscall.ENOTDIR)
	EPERM   = Status(syscall.EPERM)
	ERANGE  = Status(syscall.ERANGE)
	EXDEV   = Status(syscall.EXDEV)
)

type Opcode int

const (
	FUSE_LOOKUP      = 1
	FUSE_FORGET      = 2
	FUSE_GETATTR     = 3
	FUSE_SETATTR     = 4
	FUSE_READLINK    = 5
	FUSE_SYMLINK     = 6
	FUSE_MKNOD       = 8
	FUSE_MKDIR       = 9
	FUSE_UNLINK      = 10
	FUSE_RMDIR       = 11
	FUSE_RENAME      = 12
	FUSE_LINK        = 13
	FUSE_OPEN        = 14
	FUSE_READ        = 15
	FUSE_WRITE       = 16
	FUSE_STATFS      = 17
	FUSE_RELEASE     = 18
	FUSE_FSYNC       = 20
	FUSE_SETXATTR    = 21
	FUSE_GETXATTR    = 22
	FUSE_LISTXATTR   = 23
	FUSE_REMOVEXATTR = 24
	FUSE_FLUSH       = 25
	FUSE_INIT        = 26
	FUSE_OPENDIR     = 27
	FUSE_READDIR     = 28
	FUSE_RELEASEDIR  = 29
	FUSE_FSYNCDIR    = 30
	FUSE_GETLK       = 31
	FUSE_SETLK       = 32
	FUSE_SETLKW      = 33
	FUSE_ACCESS      = 34
	FUSE_CREATE      = 35
	FUSE_INTERRUPT   = 36
	FUSE_BMAP        = 37
	FUSE_DESTROY     = 38
	FUSE_IOCTL       = 39
	FUSE_POLL        = 40

	CUSE_INIT = 4096
)

type NotifyCode int

const (
	FUSE_NOTIFY_POLL        = 1
	FUSE_NOTIFY_INVAL_INODE = 2
	FUSE_NOTIFY_INVAL_ENTRY = 3
	FUSE_NOTIFY_CODE_MAX    = 4
)

type Attr struct {
	Ino       uint64
	Size      uint64
	Blocks    uint64
	Atime     uint64
	Mtime     uint64
	Ctime     uint64
	Atimensec uint32
	Mtimensec uint32
	Ctimensec uint32
	Mode      uint32
	Nlink     uint32
	Owner
	Rdev    uint32
	Blksize uint32
	Padding uint32
}

type Owner struct {
	Uid uint32
	Gid uint32
}

type Identity struct {
	Owner
	Pid uint32
}

type Kstatfs struct {
	Blocks  uint64
	Bfree   uint64
	Bavail  uint64
	Files   uint64
	Ffree   uint64
	Bsize   uint32
	NameLen uint32
	Frsize  uint32
	Padding uint32
	Spare   [6]uint32
}

type FileLock struct {
	Start uint64
	End   uint64
	Typ   uint32
	Pid   uint32
}

type EntryOut struct {
	NodeId         uint64
	Generation     uint64
	EntryValid     uint64
	AttrValid      uint64
	EntryValidNsec uint32
	AttrValidNsec  uint32
	Attr
}

type ForgetIn struct {
	Nlookup uint64
}

type GetAttrIn struct {
	GetAttrFlags uint32
	Dummy        uint32
	Fh           uint64
}

type AttrOut struct {
	AttrValid     uint64
	AttrValidNsec uint32
	Dummy         uint32
	Attr
}

type MknodIn struct {
	Mode    uint32
	Rdev    uint32
	Umask   uint32
	Padding uint32
}

type MkdirIn struct {
	Mode  uint32
	Umask uint32
}

type RenameIn struct {
	Newdir uint64
}

type LinkIn struct {
	Oldnodeid uint64
}

type SetAttrIn struct {
	Valid     uint32
	Padding   uint32
	Fh        uint64
	Size      uint64
	LockOwner uint64
	Atime     uint64
	Mtime     uint64
	Unused2   uint64
	Atimensec uint32
	Mtimensec uint32
	Unused3   uint32
	Mode      uint32
	Unused4   uint32
	Owner
	Unused5 uint32
}

type OpenIn struct {
	Flags  uint32
	Unused uint32
}

type CreateIn struct {
	Flags   uint32
	Mode    uint32
	Umask   uint32
	Padding uint32
}

type OpenOut struct {
	Fh        uint64
	OpenFlags uint32
	Padding   uint32
}

type CreateOut struct {
	Entry EntryOut
	Open  OpenOut
}

type ReleaseIn struct {
	Fh           uint64
	Flags        uint32
	ReleaseFlags uint32
	LockOwner    uint64
}

type FlushIn struct {
	Fh        uint64
	Unused    uint32
	Padding   uint32
	LockOwner uint64
}

type ReadIn struct {
	Fh        uint64
	Offset    uint64
	Size      uint32
	ReadFlags uint32
	LockOwner uint64
	Flags     uint32
	Padding   uint32
}

type WriteIn struct {
	Fh         uint64
	Offset     uint64
	Size       uint32
	WriteFlags uint32
	LockOwner  uint64
	Flags      uint32
	Padding    uint32
}

type WriteOut struct {
	Size    uint32
	Padding uint32
}


type StatfsOut struct {
	St Kstatfs
}

type FsyncIn struct {
	Fh         uint64
	FsyncFlags uint32
	Padding    uint32
}

type SetXAttrIn struct {
	Size  uint32
	Flags uint32
}

type GetXAttrIn struct {
	Size    uint32
	Padding uint32
}

type GetXAttrOut struct {
	Size    uint32
	Padding uint32
}

type LkIn struct {
	Fh      uint64
	Owner   uint64
	Lk      FileLock
	LkFlags uint32
	Padding uint32
}

type LkOut struct {
	Lk FileLock
}

type AccessIn struct {
	Mask    uint32
	Padding uint32
}

type InitIn struct {
	Major        uint32
	Minor        uint32
	MaxReadAhead uint32
	Flags        uint32
}

type InitOut struct {
	Major               uint32
	Minor               uint32
	MaxReadAhead        uint32
	Flags               uint32
	MaxBackground       uint16
	CongestionThreshold uint16
	MaxWrite            uint32
}

type CuseInitIn struct {
	Major  uint32
	Minor  uint32
	Unused uint32
	Flags  uint32
}

type CuseInitOut struct {
	Major    uint32
	Minor    uint32
	Unused   uint32
	Flags    uint32
	MaxRead  uint32
	MaxWrite uint32
	DevMajor uint32
	DevMinor uint32
	Spare    [10]uint32
}

type InterruptIn struct {
	Unique uint64
}

type BmapIn struct {
	Block     uint64
	Blocksize uint32
	Padding   uint32
}

type BmapOut struct {
	Block uint64
}

type IoctlIn struct {
	Fh      uint64
	Flags   uint32
	Cmd     uint32
	Arg     uint64
	InSize  uint32
	OutSize uint32
}

type IoctlOut struct {
	Result  int32
	Flags   uint32
	InIovs  uint32
	OutIovs uint32
}

type PollIn struct {
	Fh      uint64
	Kh      uint64
	Flags   uint32
	Padding uint32
}

type PollOut struct {
	Revents uint32
	Padding uint32
}

type NotifyPollWakeupOut struct {
	Kh uint64
}

type InHeader struct {
	Length uint32
	Opcode uint32
	Unique uint64
	NodeId uint64
	Identity
	Padding uint32
}

const SizeOfOutHeader = 16

type OutHeader struct {
	Length uint32
	Status Status
	Unique uint64
}

type Dirent struct {
	Ino     uint64
	Off     uint64
	NameLen uint32
	Typ     uint32
}

type NotifyInvalInodeOut struct {
	Ino    uint64
	Off    int64
	Length int64
}

type NotifyInvalEntryOut struct {
	Parent  uint64
	NameLen uint32
	Padding uint32
}


////////////////////////////////////////////////////////////////
// Types for users to implement.

// This is the interface to the file system, mirroring the interface from
//
//   /usr/include/fuse/fuse_lowlevel.h
//
// Typically, each call happens in its own goroutine, so any global data should be
// made thread-safe.
type RawFileSystem interface {
	Init(h *InHeader, input *InitIn) (out *InitOut, code Status)
	Destroy(h *InHeader, input *InitIn)

	Lookup(header *InHeader, name string) (out *EntryOut, status Status)
	Forget(header *InHeader, input *ForgetIn)

	GetAttr(header *InHeader, input *GetAttrIn) (out *AttrOut, code Status)
	SetAttr(header *InHeader, input *SetAttrIn) (out *AttrOut, code Status)

	Readlink(header *InHeader) (out []byte, code Status)
	Mknod(header *InHeader, input *MknodIn, name string) (out *EntryOut, code Status)
	Mkdir(header *InHeader, input *MkdirIn, name string) (out *EntryOut, code Status)
	Unlink(header *InHeader, name string) (code Status)
	Rmdir(header *InHeader, name string) (code Status)

	Symlink(header *InHeader, pointedTo string, linkName string) (out *EntryOut, code Status)

	Rename(header *InHeader, input *RenameIn, oldName string, newName string) (code Status)
	Link(header *InHeader, input *LinkIn, filename string) (out *EntryOut, code Status)

	GetXAttr(header *InHeader, attr string) (data []byte, code Status)

	// Unused:
	SetXAttr(header *InHeader, input *SetXAttrIn) Status

	Access(header *InHeader, input *AccessIn) (code Status)
	Create(header *InHeader, input *CreateIn, name string) (flags uint32, fuseFile RawFuseFile, out *EntryOut, code Status)
	Bmap(header *InHeader, input *BmapIn) (out *BmapOut, code Status)
	Ioctl(header *InHeader, input *IoctlIn) (out *IoctlOut, code Status)
	Poll(header *InHeader, input *PollIn) (out *PollOut, code Status)

	// The return flags are FOPEN_xx.
	Open(header *InHeader, input *OpenIn) (flags uint32, fuseFile RawFuseFile, status Status)
	OpenDir(header *InHeader, input *OpenIn) (flags uint32, fuseFile RawFuseDir, status Status)

	Release(header *InHeader, f RawFuseFile)
	ReleaseDir(header *InHeader, f RawFuseDir)
}

type RawFuseFile interface {
	Read(*ReadIn, *BufferPool) ([]byte, Status)
	// u32 <-> u64 ?
	Write(*WriteIn, []byte) (written uint32, code Status)
	Flush() Status
	Release()
	Fsync(*FsyncIn) (code Status)
}

type RawFuseDir interface {
	ReadDir(input *ReadIn) (*DirEntryList, Status)
	ReleaseDir()
	FsyncDir(input *FsyncIn) (code Status)
}

type PathFilesystem interface {
	GetAttr(name string) (*Attr, Status)
	Readlink(name string) (string, Status)
	Mknod(name string, mode uint32, dev uint32) Status
	Mkdir(name string, mode uint32) Status
	Unlink(name string) (code Status)
	Rmdir(name string) (code Status)
	Symlink(value string, linkName string) (code Status)
	Rename(oldName string, newName string) (code Status)
	Link(oldName string, newName string) (code Status)
	Chmod(name string, mode uint32) (code Status)
	Chown(name string, uid uint32, gid uint32) (code Status)
	Truncate(name string, offset uint64) (code Status)
	Open(name string, flags uint32) (file RawFuseFile, code Status)

	GetXAttr(name string, attribute string) (data []byte, code Status)

	// Where to hook up statfs?
	//
	// Unimplemented:
	// RemoveXAttr, SetXAttr, GetXAttr, ListXAttr.

	OpenDir(name string) (stream chan DirEntry, code Status)

	// TODO - what is a good interface?
	Mount(connector *PathFileSystemConnector) Status
	Unmount()

	Access(name string, mode uint32) (code Status)
	Create(name string, flags uint32, mode uint32) (file RawFuseFile, code Status)
	Utimens(name string, AtimeNs uint64, CtimeNs uint64) (code Status)

	// unimplemented: poll, ioctl, bmap.
}

// Include this method in your implementation to inherit default nop
// implementations.

type DefaultRawFuseDir struct{}
type DefaultPathFilesystem struct{}
type DefaultRawFuseFile struct{}
type DefaultRawFuseFileSystem struct{}
