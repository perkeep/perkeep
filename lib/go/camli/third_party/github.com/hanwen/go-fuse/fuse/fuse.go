package fuse

import (
	"bytes"
	"encoding/binary"
	"expvar"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
)

// TODO make generic option setting.
const (
	// bufSize should be a power of two to minimize lossage in
	// BufferPool.
	bufSize = (1 << 16)
	maxRead = bufSize - PAGESIZE
)

type Empty interface{}

////////////////////////////////////////////////////////////////
// State related to this mount point.

type fuseRequest struct {
	inputBuf []byte

	// These split up inputBuf.
	inHeader InHeader
	arg      *bytes.Buffer

	// Data for the output.
	data     interface{}
	status   Status
	flatData []byte

	// The stuff we send back to the kernel.
	serialized [][]byte

	// Start timestamp for timing info.
	startNs int64
	dispatchNs int64
	preWriteNs int64
}

// TODO - should gather stats and expose those for performance tuning.
type MountState struct {
	// We should store the RawFuseFile/Dirs on the Go side,
	// otherwise our files may be GCd.  Here, the index is the Fh
	// field

	openedFiles      map[uint64]RawFuseFile
	openedFilesMutex sync.RWMutex
	nextFreeFile     uint64

	openedDirs      map[uint64]RawFuseDir
	openedDirsMutex sync.RWMutex
	nextFreeDir     uint64

	// Empty if unmounted.
	mountPoint string
	fileSystem RawFileSystem

	// I/O with kernel and daemon.
	mountFile     *os.File
	
	errorChannel  chan os.Error

	// Run each operation in its own Go-routine.
	threaded bool

	// Dump debug info onto stdout.
	Debug bool

	// For efficient reads.
	buffers *BufferPool

	statisticsMutex    sync.Mutex
	operationCounts    map[string]int64
	operationLatencies map[string]int64
}

func (me *MountState) RegisterFile(file RawFuseFile) uint64 {
	me.openedFilesMutex.Lock()
	defer me.openedFilesMutex.Unlock()
	// We will be screwed if nextFree ever wraps.
	me.nextFreeFile++
	index := me.nextFreeFile
	me.openedFiles[index] = file
	return index
}

func (me *MountState) FindFile(index uint64) RawFuseFile {
	me.openedFilesMutex.RLock()
	defer me.openedFilesMutex.RUnlock()
	return me.openedFiles[index]
}

func (me *MountState) UnregisterFile(handle uint64) {
	me.openedFilesMutex.Lock()
	defer me.openedFilesMutex.Unlock()
	me.openedFiles[handle] = nil, false
}

func (me *MountState) RegisterDir(dir RawFuseDir) uint64 {
	me.openedDirsMutex.Lock()
	defer me.openedDirsMutex.Unlock()
	me.nextFreeDir++
	index := me.nextFreeDir
	me.openedDirs[index] = dir
	return index
}

func (me *MountState) FindDir(index uint64) RawFuseDir {
	me.openedDirsMutex.RLock()
	defer me.openedDirsMutex.RUnlock()
	return me.openedDirs[index]
}

func (me *MountState) UnregisterDir(handle uint64) {
	me.openedDirsMutex.Lock()
	defer me.openedDirsMutex.Unlock()
	me.openedDirs[handle] = nil, false
}

// Mount filesystem on mountPoint.
//
// If threaded is set, each filesystem operation executes in a
// separate goroutine, and errors and writes are done asynchronously
// using channels.
//
// TODO - error handling should perhaps be user-serviceable.
func (me *MountState) Mount(mountPoint string) os.Error {
	file, mp, err := mount(mountPoint)
	if err != nil {
		return err
	}
	me.mountPoint = mp
	me.mountFile = file

	me.operationCounts = make(map[string]int64)
	me.operationLatencies = make(map[string]int64)
	return nil
}

// Normally, callers should run loop() and wait for FUSE to exit, but
// tests will want to run this in a goroutine.
func (me *MountState) Loop(threaded bool) {
	me.threaded = threaded
	if me.threaded {
		me.errorChannel = make(chan os.Error, 100)
		go me.DefaultErrorHandler()
	}

	me.loop()

	if me.threaded {
		close(me.errorChannel)
	}
}

func (me *MountState) Unmount() os.Error {
	// Todo: flush/release all files/dirs?
	result := unmount(me.mountPoint)
	if result == nil {
		me.mountPoint = ""
	}
	return result
}

func (me *MountState) DefaultErrorHandler() {
	for err := range me.errorChannel {
		if err == os.EOF || err == nil {
			break
		}
		log.Println("error: ", err)
	}
}

func (me *MountState) Error(err os.Error) {
	// It is safe to do errors unthreaded, since the logger is thread-safe.
	if !me.threaded || me.Debug {
		log.Println("error: ", err)
	} else {
		me.errorChannel <- err
	}
}

func (me *MountState) Write(req *fuseRequest) {
	if req.serialized == nil {
		return
	}

	_, err := Writev(me.mountFile.Fd(), req.serialized)
	if err != nil {
		me.Error(os.NewError(fmt.Sprintf("writer: Writev %v failed, err: %v", req.serialized, err)))
	}

	me.discardFuseRequest(req)
}

func NewMountState(fs RawFileSystem) *MountState {
	me := new(MountState)
	me.openedDirs = make(map[uint64]RawFuseDir)
	me.openedFiles = make(map[uint64]RawFuseFile)
	me.mountPoint = ""
	me.fileSystem = fs
	me.buffers = NewBufferPool()
	return me

}

// TODO - have more statistics.
func (me *MountState) Latencies() map[string]float64 {
	me.statisticsMutex.Lock()
	defer me.statisticsMutex.Unlock()

	r := make(map[string]float64)
	for k, v := range me.operationCounts {
		r[k] = float64(me.operationLatencies[k]) / float64(v)
	}

	return r
}

func (me *MountState) OperationCounts() map[string]int64 {
	me.statisticsMutex.Lock()
	defer me.statisticsMutex.Unlock()

	r := make(map[string]int64)
	for k, v := range me.operationCounts {
		r[k] = v
	}
	return r
}

func (me *MountState) Stats() string {
	var lines []string

	// TODO - bufferpool should use expvar.
	lines = append(lines,
		fmt.Sprintf("buffers: %v", me.buffers.String()))

	for v := range expvar.Iter() {
		if strings.HasPrefix(v.Key, "mount") {
			lines = append(lines, fmt.Sprintf("%v: %v\n", v.Key, v.Value))
		}
	}
	return strings.Join(lines, "\n")
}

////////////////////////////////////////////////////////////////
// Logic for the control loop.

func (me *MountState) newFuseRequest() (*fuseRequest) {
	req := new(fuseRequest)
	req.status = OK
	req.inputBuf = me.buffers.AllocBuffer(bufSize)
	return req
}

func (me *MountState) readRequest(req *fuseRequest) os.Error {
	n, err := me.mountFile.Read(req.inputBuf)
	// If we start timing before the read, we may take into
	// account waiting for input into the timing.
	req.startNs = time.Nanoseconds()
	req.inputBuf = req.inputBuf[0:n]
	return err
}

func (me *MountState) discardFuseRequest(req *fuseRequest) {
	endNs := time.Nanoseconds() 
	dt := endNs - req.startNs

	me.statisticsMutex.Lock()
	defer me.statisticsMutex.Unlock()

	opname := operationName(req.inHeader.Opcode)
	key := opname
	me.operationCounts[key] += 1
	me.operationLatencies[key] += dt / 1e6

	key += "-dispatch" 
	me.operationLatencies[key] += (req.dispatchNs - req.startNs) / 1e6	
	me.operationCounts[key] += 1 

	key = opname + "-write" 
	me.operationLatencies[key] += (endNs - req.preWriteNs) / 1e6	
	me.operationCounts[key] += 1 

	me.buffers.FreeBuffer(req.inputBuf)
	me.buffers.FreeBuffer(req.flatData)
}

func (me *MountState) loop() {
	// See fuse_kern_chan_receive()
	for {
		req := me.newFuseRequest()

		err := me.readRequest(req)
		if err != nil {
			errNo := OsErrorToFuseError(err)

			// Retry.
			if errNo == syscall.ENOENT {
				me.discardFuseRequest(req)
				continue
			}

			// According to fuse_chan_receive()
			if errNo == syscall.ENODEV {
				break
			}

			// What I see on linux-x86 2.6.35.10.
			if errNo == syscall.ENOSYS {
				break
			}

			readErr := os.NewError(fmt.Sprintf("Failed to read from fuse conn: %v", err))
			me.Error(readErr)
			break
		}

		if me.threaded {
			go me.handle(req)
		} else {
			me.handle(req)
		}
	}
	me.mountFile.Close()
}

func (me *MountState) handle(req *fuseRequest) {
	req.dispatchNs = time.Nanoseconds()
	req.arg = bytes.NewBuffer(req.inputBuf)
	err := binary.Read(req.arg, binary.LittleEndian, &req.inHeader)
	if err == os.EOF {
		err = os.NewError(fmt.Sprintf("MountPoint, handle: can't read a header, in_data: %v", req.inputBuf))
	}
	if err != nil {
		me.Error(err)
		return
	}
	me.dispatch(req)
	req.preWriteNs = time.Nanoseconds()
	me.Write(req)
}


func (me *MountState) dispatch(req *fuseRequest) {
	// TODO - would be nice to remove this logging from the critical path.
	h := &req.inHeader

	input := newInput(h.Opcode)
	if input != nil && !parseLittleEndian(req.arg, input) {
		req.status = EIO
		serialize(req, me.Debug)
		return
	}

	var out Empty
	var status Status = OK
	fs := me.fileSystem

	filename := ""
	// Perhaps a map is faster?
	if h.Opcode == FUSE_UNLINK || h.Opcode == FUSE_RMDIR ||
		h.Opcode == FUSE_LOOKUP || h.Opcode == FUSE_MKDIR ||
		h.Opcode == FUSE_MKNOD || h.Opcode == FUSE_CREATE ||
		h.Opcode == FUSE_LINK {
		filename = strings.TrimRight(string(req.arg.Bytes()), "\x00")
	}
	if me.Debug {
		nm := ""
		if filename != "" {
			nm = "n: '" + filename + "'"
		}
		log.Printf("Dispatch: %v, NodeId: %v %s\n", operationName(h.Opcode), h.NodeId, nm)
	}

	// Follow ordering of fuse_lowlevel.h.
	switch h.Opcode {
	case FUSE_INIT:
		out, status = initFuse(me, h, input.(*InitIn))
	case FUSE_DESTROY:
		fs.Destroy(h, input.(*InitIn))
	case FUSE_LOOKUP:
		out, status = fs.Lookup(h, filename)
	case FUSE_FORGET:
		fs.Forget(h, input.(*ForgetIn))
		// If we try to write OK, nil, we will get
		// error:  writer: Writev [[16 0 0 0 0 0 0 0 17 0 0 0 0 0 0 0]]
		// failed, err: writev: no such file or directory
		me.discardFuseRequest(req)
		return
	case FUSE_GETATTR:
		// TODO - if input.Fh is set, do file.GetAttr
		out, status = fs.GetAttr(h, input.(*GetAttrIn))
	case FUSE_SETATTR:
		out, status = doSetattr(me, h, input.(*SetAttrIn))
	case FUSE_READLINK:
		out, status = fs.Readlink(h)
	case FUSE_MKNOD:
		out, status = fs.Mknod(h, input.(*MknodIn), filename)
	case FUSE_MKDIR:
		out, status = fs.Mkdir(h, input.(*MkdirIn), filename)
	case FUSE_UNLINK:
		status = fs.Unlink(h, filename)
	case FUSE_RMDIR:
		status = fs.Rmdir(h, filename)
	case FUSE_SYMLINK:
		filenames := strings.Split(string(req.arg.Bytes()), "\x00", 3)
		if len(filenames) >= 2 {
			out, status = fs.Symlink(h, filenames[1], filenames[0])
		} else {
			status = EIO
		}
	case FUSE_RENAME:
		filenames := strings.Split(string(req.arg.Bytes()), "\x00", 3)
		if len(filenames) >= 2 {
			status = fs.Rename(h, input.(*RenameIn), filenames[0], filenames[1])
		} else {
			status = EIO
		}
	case FUSE_LINK:
		out, status = fs.Link(h, input.(*LinkIn), filename)
	case FUSE_OPEN:
		out, status = doOpen(me, h, input.(*OpenIn))
	case FUSE_READ:
		req.flatData, status = doRead(me, h, input.(*ReadIn), me.buffers)
	case FUSE_WRITE:
		out, status = doWrite(me, h, input.(*WriteIn), req.arg.Bytes())
	case FUSE_FLUSH:
		out, status = doFlush(me, h, input.(*FlushIn))
	case FUSE_RELEASE:
		out, status = doRelease(me, h, input.(*ReleaseIn))
	case FUSE_FSYNC:
		status = doFsync(me, h, input.(*FsyncIn))
	case FUSE_OPENDIR:
		out, status = doOpenDir(me, h, input.(*OpenIn))
	case FUSE_READDIR:
		out, status = doReadDir(me, h, input.(*ReadIn))
	case FUSE_RELEASEDIR:
		out, status = doReleaseDir(me, h, input.(*ReleaseIn))
	case FUSE_FSYNCDIR:
		// todo- check input type.
		status = doFsyncDir(me, h, input.(*FsyncIn))

	// TODO - implement XAttr routines.
	// case FUSE_SETXATTR:
	//	status = fs.SetXAttr(h, input.(*SetXAttrIn))
	// case FUSE_GETXATTR:
	//	out, status = fs.GetXAttr(h, input.(*GetXAttrIn))
	// case FUSE_LISTXATTR:
	// case FUSE_REMOVEXATTR

	case FUSE_ACCESS:
		status = fs.Access(h, input.(*AccessIn))
	case FUSE_CREATE:
		out, status = doCreate(me, h, input.(*CreateIn), filename)

	// TODO - implement file locking.
	// case FUSE_SETLK
	// case FUSE_SETLKW
	case FUSE_BMAP:
		out, status = fs.Bmap(h, input.(*BmapIn))
	case FUSE_IOCTL:
		out, status = fs.Ioctl(h, input.(*IoctlIn))
	case FUSE_POLL:
		out, status = fs.Poll(h, input.(*PollIn))
	// TODO - figure out how to support this
	// case FUSE_INTERRUPT
	default:
		me.Error(os.NewError(fmt.Sprintf("Unsupported OpCode: %d=%v", h.Opcode, operationName(h.Opcode))))
		req.status = ENOSYS
		serialize(req, me.Debug)
		return
	}

	req.status = status
	req.data = out

	serialize(req, me.Debug)
}

func serialize(req *fuseRequest, debug bool) {
	out_data := make([]byte, 0)
	b := new(bytes.Buffer)
	if req.data != nil && req.status == OK {
		err := binary.Write(b, binary.LittleEndian, req.data)
		if err == nil {
			out_data = b.Bytes()
		} else {
			panic(fmt.Sprintf("Can't serialize out: %v, err: %v", req.data, err))
		}
	}

	var hOut OutHeader
	hOut.Unique = req.inHeader.Unique
	hOut.Status = -req.status
	hOut.Length = uint32(len(out_data) + SizeOfOutHeader + len(req.flatData))

	b = new(bytes.Buffer)
	err := binary.Write(b, binary.LittleEndian, &hOut)
	if err != nil {
		panic("Can't serialize OutHeader")
	}

	req.serialized = [][]byte{b.Bytes(), out_data, req.flatData}
	if debug {
		val := fmt.Sprintf("%v", req.data)
		max := 1024
		if len(val) > max {
			val = val[:max] + fmt.Sprintf(" ...trimmed (response size %d)", hOut.Length)
		}

		log.Printf("Serialize: %v code: %v value: %v flat: %d\n",
			operationName(req.inHeader.Opcode), req.status, val, len(req.flatData))
	}
}

func initFuse(state *MountState, h *InHeader, input *InitIn) (Empty, Status) {
	out, initStatus := state.fileSystem.Init(h, input)
	if initStatus != OK {
		return nil, initStatus
	}

	if input.Major != FUSE_KERNEL_VERSION {
		fmt.Printf("Major versions does not match. Given %d, want %d\n", input.Major, FUSE_KERNEL_VERSION)
		return nil, EIO
	}
	if input.Minor < FUSE_KERNEL_MINOR_VERSION {
		fmt.Printf("Minor version is less than we support. Given %d, want at least %d\n", input.Minor, FUSE_KERNEL_MINOR_VERSION)
		return nil, EIO
	}

	out.Major = FUSE_KERNEL_VERSION
	out.Minor = FUSE_KERNEL_MINOR_VERSION
	out.MaxReadAhead = input.MaxReadAhead
	out.Flags = FUSE_ASYNC_READ | FUSE_POSIX_LOCKS | FUSE_BIG_WRITES

	out.MaxWrite = maxRead

	return out, OK
}

////////////////////////////////////////////////////////////////
// Handling files.

func doOpen(state *MountState, header *InHeader, input *OpenIn) (genericOut Empty, code Status) {
	flags, fuseFile, status := state.fileSystem.Open(header, input)
	if status != OK {
		return nil, status
	}
	if fuseFile == nil {
		fmt.Println("fuseFile should not be nil.")
	}
	out := new(OpenOut)
	out.Fh = state.RegisterFile(fuseFile)
	out.OpenFlags = flags
	return out, status
}

func doCreate(state *MountState, header *InHeader, input *CreateIn, name string) (genericOut Empty, code Status) {
	flags, fuseFile, entry, status := state.fileSystem.Create(header, input, name)
	if status != OK {
		return nil, status
	}
	if fuseFile == nil {
		fmt.Println("fuseFile should not be nil.")
	}
	out := new(CreateOut)
	out.Entry = *entry
	out.Open.Fh = state.RegisterFile(fuseFile)
	out.Open.OpenFlags = flags
	return out, status
}

func doRelease(state *MountState, header *InHeader, input *ReleaseIn) (out Empty, code Status) {
	f := state.FindFile(input.Fh)
	state.fileSystem.Release(header, f)
	f.Release()
	state.UnregisterFile(input.Fh)
	return nil, OK
}

func doRead(state *MountState, header *InHeader, input *ReadIn, buffers *BufferPool) (out []byte, code Status) {
	output, code := state.FindFile(input.Fh).Read(input, buffers)
	return output, code
}

func doWrite(state *MountState, header *InHeader, input *WriteIn, data []byte) (out WriteOut, code Status) {
	n, status := state.FindFile(input.Fh).Write(input, data)
	out.Size = n
	return out, status
}

func doFsync(state *MountState, header *InHeader, input *FsyncIn) (code Status) {
	return state.FindFile(input.Fh).Fsync(input)
}

func doFlush(state *MountState, header *InHeader, input *FlushIn) (out Empty, code Status) {
	return nil, state.FindFile(input.Fh).Flush()
}

func doSetattr(state *MountState, header *InHeader, input *SetAttrIn) (out *AttrOut, code Status) {
	// TODO - if Fh != 0, we should do a FSetAttr instead.
	return state.fileSystem.SetAttr(header, input)
}

////////////////////////////////////////////////////////////////
// Handling directories

func doReleaseDir(state *MountState, header *InHeader, input *ReleaseIn) (out Empty, code Status) {
	d := state.FindDir(input.Fh)
	state.fileSystem.ReleaseDir(header, d)
	d.ReleaseDir()
	state.UnregisterDir(input.Fh)
	return nil, OK
}

func doOpenDir(state *MountState, header *InHeader, input *OpenIn) (genericOut Empty, code Status) {
	flags, fuseDir, status := state.fileSystem.OpenDir(header, input)
	if status != OK {
		return nil, status
	}

	out := new(OpenOut)
	out.Fh = state.RegisterDir(fuseDir)
	out.OpenFlags = flags
	return out, status
}

func doReadDir(state *MountState, header *InHeader, input *ReadIn) (out Empty, code Status) {
	dir := state.FindDir(input.Fh)
	entries, code := dir.ReadDir(input)
	if entries == nil {
		var emptyBytes []byte
		return emptyBytes, code
	}
	return entries.Bytes(), code
}

func doFsyncDir(state *MountState, header *InHeader, input *FsyncIn) (code Status) {
	return state.FindDir(input.Fh).FsyncDir(input)
}
