package fuse

import (
	"sync"
	"time"
)

// TimingPathFilesystem is a wrapper to collect timings for a PathFilesystem
type TimingPathFilesystem struct {
	original PathFilesystem

	statisticsLock sync.Mutex
	latencies      map[string]int64
	counts         map[string]int64
}

func NewTimingPathFilesystem(fs PathFilesystem) *TimingPathFilesystem {
	t := new(TimingPathFilesystem)
	t.original = fs
	t.latencies = make(map[string]int64)
	t.counts = make(map[string]int64)
	return t
}

func (me *TimingPathFilesystem) startTimer(name string) (closure func()) {
	start := time.Nanoseconds()

	return func() {
		dt := (time.Nanoseconds() - start) / 1e6
		me.statisticsLock.Lock()
		defer me.statisticsLock.Unlock()

		me.counts[name] += 1
		me.latencies[name] += dt
	}
}

func (me *TimingPathFilesystem) Latencies() map[string]float64 {
	me.statisticsLock.Lock()
	defer me.statisticsLock.Unlock()

	r := make(map[string]float64)
	for k, v := range me.counts {
		r[k] = float64(me.latencies[k]) / float64(v)
	}
	return r
}

func (me *TimingPathFilesystem) GetAttr(name string) (*Attr, Status) {
	defer me.startTimer("GetAttr")()
	return me.original.GetAttr(name)
}

func (me *TimingPathFilesystem) Readlink(name string) (string, Status) {
	defer me.startTimer("Readlink")()
	return me.original.Readlink(name)
}

func (me *TimingPathFilesystem) Mknod(name string, mode uint32, dev uint32) Status {
	defer me.startTimer("Mknod")()
	return me.original.Mknod(name, mode, dev)
}

func (me *TimingPathFilesystem) Mkdir(name string, mode uint32) Status {
	defer me.startTimer("Mkdir")()
	return me.original.Mkdir(name, mode)
}

func (me *TimingPathFilesystem) Unlink(name string) (code Status) {
	defer me.startTimer("Unlink")()
	return me.original.Unlink(name)
}

func (me *TimingPathFilesystem) Rmdir(name string) (code Status) {
	defer me.startTimer("Rmdir")()
	return me.original.Rmdir(name)
}

func (me *TimingPathFilesystem) Symlink(value string, linkName string) (code Status) {
	defer me.startTimer("Symlink")()
	return me.original.Symlink(value, linkName)
}

func (me *TimingPathFilesystem) Rename(oldName string, newName string) (code Status) {
	defer me.startTimer("Rename")()
	return me.original.Rename(oldName, newName)
}

func (me *TimingPathFilesystem) Link(oldName string, newName string) (code Status) {
	defer me.startTimer("Link")()
	return me.original.Link(oldName, newName)
}

func (me *TimingPathFilesystem) Chmod(name string, mode uint32) (code Status) {
	defer me.startTimer("Chmod")()
	return me.original.Chmod(name, mode)
}

func (me *TimingPathFilesystem) Chown(name string, uid uint32, gid uint32) (code Status) {
	defer me.startTimer("Chown")()
	return me.original.Chown(name, uid, gid)
}

func (me *TimingPathFilesystem) Truncate(name string, offset uint64) (code Status) {
	defer me.startTimer("Truncate")()
	return me.original.Truncate(name, offset)
}

func (me *TimingPathFilesystem) Open(name string, flags uint32) (file RawFuseFile, code Status) {
	defer me.startTimer("Open")()
	return me.original.Open(name, flags)
}

func (me *TimingPathFilesystem) OpenDir(name string) (stream chan DirEntry, status Status) {
	defer me.startTimer("OpenDir")()
	return me.original.OpenDir(name)
}

func (me *TimingPathFilesystem) Mount(conn *PathFileSystemConnector) Status {
	defer me.startTimer("Mount")()
	return me.original.Mount(conn)
}

func (me *TimingPathFilesystem) Unmount() {
	defer me.startTimer("Unmount")()
	me.original.Unmount()
}

func (me *TimingPathFilesystem) Access(name string, mode uint32) (code Status) {
	defer me.startTimer("Access")()
	return me.original.Access(name, mode)
}

func (me *TimingPathFilesystem) Create(name string, flags uint32, mode uint32) (file RawFuseFile, code Status) {
	defer me.startTimer("Create")()
	return me.original.Create(name, flags, mode)
}

func (me *TimingPathFilesystem) Utimens(name string, AtimeNs uint64, CtimeNs uint64) (code Status) {
	defer me.startTimer("Utimens")()
	return me.original.Utimens(name, AtimeNs, CtimeNs)
}
