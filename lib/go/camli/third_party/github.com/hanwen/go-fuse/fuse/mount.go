package fuse

// Written with a look to http://ptspts.blogspot.com/2009/11/fuse-protocol-tutorial-for-linux-26.html
import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

func Socketpair(network string) (l, r *os.File, err os.Error) {
	var domain int
	var typ int
	switch network {
	default:
		panic("unknown network " + network)
	case "unix":
		domain = syscall.AF_UNIX
		typ = syscall.SOCK_STREAM
	case "unixgram":
		domain = syscall.AF_UNIX
		typ = syscall.SOCK_SEQPACKET
	}
	fd, errno := syscall.Socketpair(domain, typ, 0)
	if errno != 0 {
		return nil, nil, os.NewSyscallError("socketpair", errno)
	}
	l = os.NewFile(fd[0], "socketpair-half1")
	r = os.NewFile(fd[1], "socketpair-half2")
	return
}

// Mount create a fuse fs on the specified mount point.  The returned
// mount point is always absolute.
func mount(mountPoint string) (f *os.File, finalMountPoint string, err os.Error) {
	local, remote, err := Socketpair("unixgram")
	if err != nil {
		return
	}

	defer local.Close()
	defer remote.Close()

	mountPoint = filepath.Clean(mountPoint)
	if !filepath.IsAbs(mountPoint) {
		cwd, err := os.Getwd()
		if err != nil {
			return
		}
		mountPoint = filepath.Clean(filepath.Join(cwd, mountPoint))
	}
	proc, err := os.StartProcess("/bin/fusermount",
		[]string{"/bin/fusermount", mountPoint},
		&os.ProcAttr{
			Env:   []string{"_FUSE_COMMFD=3"},
			Files: []*os.File{os.Stdin, os.Stdout, os.Stderr, remote}})

	if err != nil {
		return
	}
	w, err := os.Wait(proc.Pid, 0)
	if err != nil {
		return
	}
	if w.ExitStatus() != 0 {
		err = os.NewError(fmt.Sprintf("fusermount exited with code %d\n", w.ExitStatus()))
		return
	}

	f, err = getFuseConn(local)
	finalMountPoint = mountPoint
	return
}

func unmount(mountPoint string) (err os.Error) {
	dir, _ := filepath.Split(mountPoint)
	proc, err := os.StartProcess("/bin/fusermount",
		[]string{"/bin/fusermount", "-u", mountPoint},
		&os.ProcAttr{Dir: dir, Files: []*os.File{nil, nil, os.Stderr}})
	if err != nil {
		return
	}
	w, err := os.Wait(proc.Pid, 0)
	if err != nil {
		return
	}
	if w.ExitStatus() != 0 {
		return os.NewError(fmt.Sprintf("fusermount -u exited with code %d\n", w.ExitStatus()))
	}
	return
}

func getFuseConn(local *os.File) (f *os.File, err os.Error) {
	var data [4]byte
	control := make([]byte, 4*256)

	// n, oobn, recvflags, from, errno  - todo: error checking.
	_, oobn, _, _,
		errno := syscall.Recvmsg(
		local.Fd(), data[:], control[:], 0)
	if errno != 0 {
		return
	}

	message := *(*syscall.Cmsghdr)(unsafe.Pointer(&control[0]))
	fd := *(*int32)(unsafe.Pointer(uintptr(unsafe.Pointer(&control[0])) + syscall.SizeofCmsghdr))

	if message.Type != 1 {
		err = os.NewError(fmt.Sprintf("getFuseConn: recvmsg returned wrong control type: %d", message.Type))
		return
	}
	if oobn <= syscall.SizeofCmsghdr {
		err = os.NewError(fmt.Sprintf("getFuseConn: too short control message. Length: %d", oobn))
		return
	}
	if fd < 0 {
		err = os.NewError(fmt.Sprintf("getFuseConn: fd < 0: %d", fd))
		return
	}
	f = os.NewFile(int(fd), "<fuseConnection>")
	return
}
