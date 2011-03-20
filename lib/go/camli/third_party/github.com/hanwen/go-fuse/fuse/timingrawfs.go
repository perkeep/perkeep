package fuse

import (
	"sync"
	"time"
)

// TimingRawFilesystem is a wrapper to collect timings for a RawFilesystem
type TimingRawFilesystem struct {
	original RawFileSystem

	statisticsLock sync.Mutex
	latencies      map[string]int64
	counts         map[string]int64
}

func NewTimingRawFilesystem(fs RawFileSystem) *TimingRawFilesystem {
	t := new(TimingRawFilesystem)
	t.original = fs
	t.latencies = make(map[string]int64)
	t.counts = make(map[string]int64)
	return t
}

func (me *TimingRawFilesystem) startTimer(name string) (closure func()) {
	start := time.Nanoseconds()

	return func() {
		dt := (time.Nanoseconds() - start) / 1e6
		me.statisticsLock.Lock()
		defer me.statisticsLock.Unlock()

		me.counts[name] += 1
		me.latencies[name] += dt
	}
}

func (me *TimingRawFilesystem) Latencies() map[string]float64 {
	me.statisticsLock.Lock()
	defer me.statisticsLock.Unlock()

	r := make(map[string]float64)
	for k, v := range me.counts {
		r[k] = float64(me.latencies[k]) / float64(v)
	}
	return r
}

func (me *TimingRawFilesystem) Init(h *InHeader, input *InitIn) (*InitOut, Status) {
	defer me.startTimer("Init")()
	return me.original.Init(h, input)
}

func (me *TimingRawFilesystem) Destroy(h *InHeader, input *InitIn) {
	defer me.startTimer("Destroy")()
	me.original.Destroy(h, input)
}

func (me *TimingRawFilesystem) Lookup(h *InHeader, name string) (out *EntryOut, code Status) {
	defer me.startTimer("Lookup")()
	return me.original.Lookup(h, name)
}

func (me *TimingRawFilesystem) Forget(h *InHeader, input *ForgetIn) {
	defer me.startTimer("Forget")()
	me.original.Forget(h, input)
}

func (me *TimingRawFilesystem) GetAttr(header *InHeader, input *GetAttrIn) (out *AttrOut, code Status) {
	defer me.startTimer("GetAttr")()
	return me.original.GetAttr(header, input)
}

func (me *TimingRawFilesystem) Open(header *InHeader, input *OpenIn) (flags uint32, fuseFile RawFuseFile, status Status) {
	defer me.startTimer("Open")()
	return me.original.Open(header, input)
}

func (me *TimingRawFilesystem) SetAttr(header *InHeader, input *SetAttrIn) (out *AttrOut, code Status) {
	defer me.startTimer("SetAttr")()
	return me.original.SetAttr(header, input)
}

func (me *TimingRawFilesystem) Readlink(header *InHeader) (out []byte, code Status) {
	defer me.startTimer("Readlink")()
	return me.original.Readlink(header)
}

func (me *TimingRawFilesystem) Mknod(header *InHeader, input *MknodIn, name string) (out *EntryOut, code Status) {
	defer me.startTimer("Mknod")()
	return me.original.Mknod(header, input, name)
}

func (me *TimingRawFilesystem) Mkdir(header *InHeader, input *MkdirIn, name string) (out *EntryOut, code Status) {
	defer me.startTimer("Mkdir")()
	return me.original.Mkdir(header, input, name)
}

func (me *TimingRawFilesystem) Unlink(header *InHeader, name string) (code Status) {
	defer me.startTimer("Unlink")()
	return me.original.Unlink(header, name)
}

func (me *TimingRawFilesystem) Rmdir(header *InHeader, name string) (code Status) {
	defer me.startTimer("Rmdir")()
	return me.original.Rmdir(header, name)
}

func (me *TimingRawFilesystem) Symlink(header *InHeader, pointedTo string, linkName string) (out *EntryOut, code Status) {
	defer me.startTimer("Symlink")()
	return me.original.Symlink(header, pointedTo, linkName)
}

func (me *TimingRawFilesystem) Rename(header *InHeader, input *RenameIn, oldName string, newName string) (code Status) {
	defer me.startTimer("Rename")()
	return me.original.Rename(header, input, oldName, newName)
}

func (me *TimingRawFilesystem) Link(header *InHeader, input *LinkIn, name string) (out *EntryOut, code Status) {
	defer me.startTimer("Link")()
	return me.original.Link(header, input, name)
}

func (me *TimingRawFilesystem) SetXAttr(header *InHeader, input *SetXAttrIn) Status {
	defer me.startTimer("SetXAttr")()
	return me.original.SetXAttr(header, input)
}

func (me *TimingRawFilesystem) GetXAttr(header *InHeader, input *GetXAttrIn) (out *GetXAttrOut, code Status) {
	defer me.startTimer("GetXAttr")()
	return me.original.GetXAttr(header, input)
}

func (me *TimingRawFilesystem) Access(header *InHeader, input *AccessIn) (code Status) {
	defer me.startTimer("Access")()
	return me.original.Access(header, input)
}

func (me *TimingRawFilesystem) Create(header *InHeader, input *CreateIn, name string) (flags uint32, fuseFile RawFuseFile, out *EntryOut, code Status) {
	defer me.startTimer("Create")()
	return me.original.Create(header, input, name)
}

func (me *TimingRawFilesystem) Bmap(header *InHeader, input *BmapIn) (out *BmapOut, code Status) {
	defer me.startTimer("Bmap")()
	return me.original.Bmap(header, input)
}

func (me *TimingRawFilesystem) Ioctl(header *InHeader, input *IoctlIn) (out *IoctlOut, code Status) {
	defer me.startTimer("Ioctl")()
	return me.original.Ioctl(header, input)
}

func (me *TimingRawFilesystem) Poll(header *InHeader, input *PollIn) (out *PollOut, code Status) {
	defer me.startTimer("Poll")()
	return me.original.Poll(header, input)
}

func (me *TimingRawFilesystem) OpenDir(header *InHeader, input *OpenIn) (flags uint32, fuseFile RawFuseDir, status Status) {
	defer me.startTimer("OpenDir")()
	return me.original.OpenDir(header, input)
}

func (me *TimingRawFilesystem) Release(header *InHeader, f RawFuseFile) {
	defer me.startTimer("Release")()
	me.original.Release(header, f)
}

func (me *TimingRawFilesystem) ReleaseDir(header *InHeader, f RawFuseDir) {
	defer me.startTimer("ReleaseDir")()
	me.original.ReleaseDir(header, f)
}

