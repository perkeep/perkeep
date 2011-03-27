package fuse

import (
	"sync"
	"time"
	"log"
	"fmt"
	"sort"
)

var _ = log.Print
var _ = fmt.Print

// TimingPathFilesystem is a wrapper to collect timings for a PathFilesystem
type TimingPathFilesystem struct {
	original PathFilesystem

	statisticsLock sync.Mutex
	latencies      map[string]int64
	counts         map[string]int64
	pathCounts     map[string]map[string]int64
}

func NewTimingPathFilesystem(fs PathFilesystem) *TimingPathFilesystem {
	t := new(TimingPathFilesystem)
	t.original = fs
	t.latencies = make(map[string]int64)
	t.counts = make(map[string]int64)
	t.pathCounts = make(map[string]map[string]int64)
	return t
}

func (me *TimingPathFilesystem) startTimer(name string, arg string) (closure func()) {
	start := time.Nanoseconds()

	return func() {
		dt := (time.Nanoseconds() - start) / 1e6
		me.statisticsLock.Lock()
		defer me.statisticsLock.Unlock()

		me.counts[name] += 1
		me.latencies[name] += dt

		m, ok := me.pathCounts[name]
		if !ok {
			m = make(map[string]int64)
			me.pathCounts[name] = m
		}
		m[arg] += 1
	}
}

func (me *TimingPathFilesystem) OperationCounts() map[string]int64 {
	me.statisticsLock.Lock()
	defer me.statisticsLock.Unlock()

	r := make(map[string]int64)
	for k, v := range me.counts {
		r[k] = v
	}
	return r
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

func (me *TimingPathFilesystem) HotPaths(operation string) (paths []string, uniquePaths int) {
	me.statisticsLock.Lock()
	defer me.statisticsLock.Unlock()

	counts := me.pathCounts[operation]
	results := make([]string, 0, len(counts))
	for k, v := range counts {
		results = append(results, fmt.Sprintf("% 9d %s", v, k))

	}
	sort.SortStrings(results)
	return results, len(counts)
}

func (me *TimingPathFilesystem) GetAttr(name string) (*Attr, Status) {
	defer me.startTimer("GetAttr", name)()
	return me.original.GetAttr(name)
}

func (me *TimingPathFilesystem) GetXAttr(name string, attr string) ([]byte, Status) {
	defer me.startTimer("GetXAttr", name)()
	return me.original.GetXAttr(name, attr)
}

func (me *TimingPathFilesystem) Readlink(name string) (string, Status) {
	defer me.startTimer("Readlink", name)()
	return me.original.Readlink(name)
}

func (me *TimingPathFilesystem) Mknod(name string, mode uint32, dev uint32) Status {
	defer me.startTimer("Mknod", name)()
	return me.original.Mknod(name, mode, dev)
}

func (me *TimingPathFilesystem) Mkdir(name string, mode uint32) Status {
	defer me.startTimer("Mkdir", name)()
	return me.original.Mkdir(name, mode)
}

func (me *TimingPathFilesystem) Unlink(name string) (code Status) {
	defer me.startTimer("Unlink", name)()
	return me.original.Unlink(name)
}

func (me *TimingPathFilesystem) Rmdir(name string) (code Status) {
	defer me.startTimer("Rmdir", name)()
	return me.original.Rmdir(name)
}

func (me *TimingPathFilesystem) Symlink(value string, linkName string) (code Status) {
	defer me.startTimer("Symlink", linkName)()
	return me.original.Symlink(value, linkName)
}

func (me *TimingPathFilesystem) Rename(oldName string, newName string) (code Status) {
	defer me.startTimer("Rename", oldName)()
	return me.original.Rename(oldName, newName)
}

func (me *TimingPathFilesystem) Link(oldName string, newName string) (code Status) {
	defer me.startTimer("Link", newName)()
	return me.original.Link(oldName, newName)
}

func (me *TimingPathFilesystem) Chmod(name string, mode uint32) (code Status) {
	defer me.startTimer("Chmod", name)()
	return me.original.Chmod(name, mode)
}

func (me *TimingPathFilesystem) Chown(name string, uid uint32, gid uint32) (code Status) {
	defer me.startTimer("Chown", name)()
	return me.original.Chown(name, uid, gid)
}

func (me *TimingPathFilesystem) Truncate(name string, offset uint64) (code Status) {
	defer me.startTimer("Truncate", name)()
	return me.original.Truncate(name, offset)
}

func (me *TimingPathFilesystem) Open(name string, flags uint32) (file RawFuseFile, code Status) {
	defer me.startTimer("Open", name)()
	return me.original.Open(name, flags)
}

func (me *TimingPathFilesystem) OpenDir(name string) (stream chan DirEntry, status Status) {
	defer me.startTimer("OpenDir", name)()
	return me.original.OpenDir(name)
}

func (me *TimingPathFilesystem) Mount(conn *PathFileSystemConnector) Status {
	defer me.startTimer("Mount", "")()
	return me.original.Mount(conn)
}

func (me *TimingPathFilesystem) Unmount() {
	defer me.startTimer("Unmount", "")()
	me.original.Unmount()
}

func (me *TimingPathFilesystem) Access(name string, mode uint32) (code Status) {
	defer me.startTimer("Access", name)()
	return me.original.Access(name, mode)
}

func (me *TimingPathFilesystem) Create(name string, flags uint32, mode uint32) (file RawFuseFile, code Status) {
	defer me.startTimer("Create", name)()
	return me.original.Create(name, flags, mode)
}

func (me *TimingPathFilesystem) Utimens(name string, AtimeNs uint64, CtimeNs uint64) (code Status) {
	defer me.startTimer("Utimens", name)()
	return me.original.Utimens(name, AtimeNs, CtimeNs)
}
