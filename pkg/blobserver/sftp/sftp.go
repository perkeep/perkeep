/*
Copyright 2018 The Perkeep Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

/*
Package sftp registers the "sftp" blobserver storage type, storing
blobs one-per-file in a forest of sharded directories to a remote SFTP
server over an SSH connection. It uses the same directory & file
structure as the "localdisk" storage type.

Example low-level config:

	"/storage/": {
	    "handler": "storage-sftp",
	    "handlerArgs": {
	         "user": "alice",
	         "addr": "10.1.2.3",
	         "dir": "/remote/path/to/store/blobs/in",
	         "serverFingerprint": "SHA256:fBkTSuUzQVnVMJ9+e74XNTCnQKSHldbfFiOI9kBMemc",

	         "pass": "s3cr3thunteR1!",
	         "passFile": "/home/alice/keys/sftp.password"
	     }
	},
*/
package sftp // import "perkeep.org/pkg/blobserver/sftp"

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"

	"go4.org/jsonconfig"
	"go4.org/syncutil"
	"go4.org/syncutil/singleflight"
	"go4.org/wkfs"
	"golang.org/x/crypto/ssh"
	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/files"
)

// Storage implements the blobserver.Storage interface using an SFTP server.
type Storage struct {
	blobserver.Storage
	blob.SubFetcher

	addr, root string
	cc         *ssh.ClientConfig

	getClientGroup singleflight.Group

	mu         sync.Mutex
	lastGet    time.Time // time last fetched
	sc         *sftp.Client
	connCloser io.Closer // ssh.Conn or net.Conn
}

// Validate we implement expected interfaces.
var (
	_ blobserver.Storage = (*Storage)(nil)
	_ blob.SubFetcher    = (*Storage)(nil)
)

func (s *Storage) String() string {
	return fmt.Sprintf("\"sftp\" file-per-blob at %s@%s, dir %s", s.cc.User, s.addr, s.root)
}

const (
	// We refuse to create a Storage when the user's ulimit is lower than
	// minFDLimit. 100 is ridiculously low, but the default value on OSX is 256, and we
	// don't want to fail by default, so our min value has to be lower than 256.
	minFDLimit         = 100
	recommendedFDLimit = 1024
)

// NewStorage returns a new SFTP storage implementation at the provided
// TCP addr (host:port) in the named directory. An empty dir means ".".
// The provided SSH client configured is required.
func NewStorage(addr, dir string, cc *ssh.ClientConfig) (*Storage, error) {
	if dir == "" {
		dir = "."
	}
	s := &Storage{
		addr: addr,
		root: dir,
		cc:   cc,
	}
	fs := files.NewStorage(sftpFS{s}, dir)
	s.Storage = fs
	s.SubFetcher = fs

	if err := s.adjustFDLimit(fs); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Storage) adjustFDLimit(fs *files.Storage) error {
	ul, err := osutil.MaxFD()
	if errors.Is(err, osutil.ErrNotSupported) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("sftp failed to determine system's file descriptor limit: %w", err)
	}
	if ul < minFDLimit {
		return fmt.Errorf("the max number of open file descriptors on your system (ulimit -n) is too low. Please fix it with 'ulimit -S -n X' with X being at least %d", recommendedFDLimit)
	}
	// Setting the gate to 80% of the ulimit, to leave a bit of room for other file ops happening in Perkeep.
	// TODO(mpl): make this used and enforced Perkeep-wide. Issue #837.
	fs.SetNewFileGate(syncutil.NewGate(int(ul * 80 / 100)))
	return nil
}

func newFromConfig(_ blobserver.Loader, config jsonconfig.Obj) (storage blobserver.Storage, err error) {
	user := config.RequiredString("user")
	dir := config.RequiredString("dir")
	addr := config.RequiredString("addr")
	pass := config.OptionalString("pass", "")
	passFile := config.OptionalString("passFile", "")
	wantFingerprint := config.RequiredString("serverFingerprint")

	if err := config.Validate(); err != nil {
		return nil, err
	}

	if pass != "" && passFile != "" {
		return nil, errors.New(`the "pass" and "passFile" options are mutually exclusive`)
	}
	if passFile != "" {
		slurp, err := wkfs.ReadFile(passFile)
		if err != nil {
			return nil, err
		}
		pass = strings.TrimSpace(string(slurp))
	}

	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, "22")
	}
	cc := &ssh.ClientConfig{
		User: user,
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			keyPrint := ssh.FingerprintSHA256(key)
			if keyPrint == wantFingerprint {
				log.Printf("sftp: connected to %s at %v@%v, with expected fingerprint %v",
					hostname, user, remote, ssh.FingerprintSHA256(key))
				return nil
			}
			if wantFingerprint == "insecure-skip-verify" {
				log.Printf(`sftp: WARNNING: using "insecure-skip-verify", connected to %s at %v@%v, with untrusted fingerprint %v`,
					hostname, user, remote, ssh.FingerprintSHA256(key))
				return nil
			}
			return fmt.Errorf(`sftp: unexpected fingerprint %q connecting to %v/%v; want %q (or "insecure-skip-verify")`,
				keyPrint, hostname, remote, wantFingerprint)
		},
		Timeout: 10 * time.Second,
	}
	if pass != "" {
		cc.Auth = []ssh.AuthMethod{ssh.Password(pass)}
	}
	return NewStorage(addr, dir, cc)
}

// markConnDead clears the cached SFTP connection after the caller detects
// a connection failure.
func (s *Storage) markConnDead() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.markConnDeadLocked()
}

func (s *Storage) markConnDeadLocked() {
	if s.connCloser != nil {
		go s.connCloser.Close()
	}
	s.sc = nil
	s.connCloser = nil
}

func (s *Storage) monitorSSHConn(wait func() error) {
	err := wait()
	log.Printf("sftp: marking SSH connection dead: %v", err)
	s.markConnDead()
}

func (s *Storage) dialSFTP() (sc *sftp.Client, waiter func() error, toClose io.Closer, err error) {
	// Special case for testing:
	if s.cc.User == "RAWSFTPNOSSH" {
		var c net.Conn
		c, err = net.Dial("tcp", s.addr)
		if err != nil {
			return
		}
		sc, err = sftp.NewClientPipe(c, c)
		if err != nil {
			go c.Close()
			return
		}
		toClose = c
		return
	}

	var pw io.WriteCloser
	var pr io.Reader

	// Another special case for testing:
	user := s.cc.User
	const sysPrefix = "use-system-ssh:"
	if after, ok := strings.CutPrefix(user, sysPrefix); ok {
		user = after
		cmd := exec.Command("ssh", user+"@"+strings.TrimSuffix(s.addr, ":22"), "-s", "sftp")
		cmd.Stderr = os.Stderr
		// get stdin and stdout
		pw, err = cmd.StdinPipe()
		if err != nil {
			err = fmt.Errorf("%v: %w", cmd.Args, err)
			return
		}
		pr, err = cmd.StdoutPipe()
		if err != nil {
			err = fmt.Errorf("%v: %w", cmd.Args, err)
			return
		}

		// start the process
		if err = cmd.Start(); err != nil {
			err = fmt.Errorf("%v: %w", cmd.Args, err)
			return
		}
		log.Printf("using sftp directly")
		sc, err = sftp.NewClientPipe(pr, pw)
		return
	}

	var sshc *ssh.Client
	sshc, err = ssh.Dial("tcp", s.addr, s.cc)
	if err != nil {
		log.Printf("sftp: Dial: %v", err)
		return
	}

	var sess *ssh.Session
	sess, err = sshc.NewSession()
	if err != nil {
		log.Printf("sftp: ssh NewSession: %v", err)
		go sshc.Close()
		return
	}
	if err = sess.RequestSubsystem("sftp"); err != nil {
		log.Printf("sftp: RequestSubsystem: %v", err)
		go sshc.Close()
		return
	}
	pw, err = sess.StdinPipe()
	if err != nil {
		go sshc.Close()
		return
	}
	pr, err = sess.StdoutPipe()
	if err != nil {
		go sshc.Close()
		return
	}

	sc, err = sftp.NewClientPipe(pr, pw)
	if err != nil {
		go sshc.Close()
		return
	}
	toClose = sshc
	waiter = sshc.Wait
	return
}

// sftp returns the *sftp.Client to the server, handling reconnects and coalesced dialing
// for concurrent callers.
func (s *Storage) sftp() (*sftp.Client, error) {
	s.mu.Lock()
	if s.sc != nil {
		defer s.mu.Unlock()
		// TODO: remove all this "lastGet" stuff once the wait
		// mechanism has test coverage and we're sending
		// periodic heartbeats over the SSH channel.
		if now := time.Now(); s.lastGet.After(now.Add(-30 * time.Second)) {
			s.lastGet = now
			return s.sc, nil
		} else {
			// It's been awhile. Let's see if it's still good.
			_, err := s.sc.Stat(".")
			if err != nil {
				s.markConnDeadLocked()
			} else {
				s.lastGet = now
				return s.sc, nil
			}
		}
	}
	s.mu.Unlock()
	ci, err := s.getClientGroup.Do("", func() (any, error) {
		sc, waiter, toClose, err := s.dialSFTP()
		if err != nil {
			return nil, err
		}
		if waiter != nil {
			go s.monitorSSHConn(waiter)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.connCloser = toClose
		s.sc = sc
		s.lastGet = time.Now()
		return sc, nil
	})
	if err != nil {
		return nil, err
	}
	return ci.(*sftp.Client), nil
}

func init() {
	blobserver.RegisterStorageConstructor("sftp", blobserver.StorageConstructor(newFromConfig))
}

type sftpFS struct {
	*Storage
}

func (s sftpFS) Remove(file string) error {
	sc, err := s.sftp()
	if err != nil {
		return err
	}
	return sc.Remove(filepath.ToSlash(file))
}

func (s sftpFS) RemoveDir(dir string) error {
	sc, err := s.sftp()
	if err != nil {
		return err
	}
	return sc.RemoveDirectory(filepath.ToSlash(dir))
}

func (s sftpFS) Open(file string) (files.ReadableFile, error) {
	sc, err := s.sftp()
	if err != nil {
		return nil, err
	}
	return sc.Open(filepath.ToSlash(file))
}

func (s sftpFS) Rename(oldname, newname string) error {
	sc, err := s.sftp()
	if err != nil {
		return err
	}
	return sc.PosixRename(filepath.ToSlash(oldname), filepath.ToSlash(newname))
}

func (s sftpFS) TempFile(dir, prefix string) (files.WritableFile, error) {
	sc, err := s.sftp()
	if err != nil {
		return nil, err
	}
	dir = filepath.ToSlash(dir)
	for range 5 {
		sufRand := make([]byte, 5)
		_, _ = rand.Read(sufRand)
		suffix := fmt.Sprintf("%x", sufRand)
		name := path.Join(dir, prefix+suffix)
		f, err := sc.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR)
		if err == nil {
			return writableFile{
				name:        name,
				WriteCloser: f,
			}, nil
		}
	}
	return nil, fmt.Errorf("sftp: failed to open temp file in %s:%s/%s with prefix %s", s.addr, s.root, dir, prefix)
}

type writableFile struct {
	io.WriteCloser
	name string
}

func (f writableFile) Name() string { return f.name }
func (f writableFile) Sync() error  { return nil } // TODO: send fsync

func (s sftpFS) ReadDirNames(dir string) ([]string, error) {
	sc, err := s.sftp()
	if err != nil {
		return nil, err
	}
	fis, err := sc.ReadDir(filepath.ToSlash(dir))
	if err != nil {
		return nil, err
	}
	// TODO: it's wasteful that we throw all this info away and
	// have the files package restat each file. Change the
	// interface or add an optional one that sftp can implement
	// and make the files package smarter about not asking for
	// duplicate work when possible.
	names := make([]string, len(fis))
	for i, fi := range fis {
		names[i] = fi.Name()
	}
	return names, nil
}

func (s sftpFS) Stat(path string) (os.FileInfo, error) {
	sc, err := s.sftp()
	if err != nil {
		return nil, err
	}
	return sc.Stat(filepath.ToSlash(path))
}

func (s sftpFS) Lstat(dir string) (os.FileInfo, error) {
	sc, err := s.sftp()
	if err != nil {
		return nil, err
	}
	return sc.Lstat(filepath.ToSlash(dir))
}

func (s sftpFS) MkdirAll(dir string, perm os.FileMode) error {
	sc, err := s.sftp()
	if err != nil {
		return err
	}

	return sc.MkdirAll(filepath.ToSlash(dir))
}
