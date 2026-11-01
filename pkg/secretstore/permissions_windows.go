/*
Copyright 2026 The Perkeep Authors.

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

package secretstore

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows FileMode permission bits are synthesized and cannot establish
// privacy. Every credential directory/file is therefore checked with native
// security descriptors. SYSTEM and Administrators are trusted OS principals,
// analogous to root on Unix; no ordinary other account may be granted access.
func currentUserSID() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String(), nil
}

func trustedSID(sid, user string) bool {
	return sid == user || sid == "S-1-5-18" || sid == "S-1-5-32-544"
}

func privateDescriptor(directory bool) (*windows.SECURITY_DESCRIPTOR, error) {
	user, err := currentUserSID()
	if err != nil {
		return nil, err
	}
	flags := ""
	if directory {
		flags = "OICI"
	}
	// P protects the DACL from inheriting broader permissions. Child files
	// (including SQLite's own journals) inherit only these private principals.
	return windows.SecurityDescriptorFromString("O:" + user + "D:P" +
		"(A;" + flags + ";FA;;;" + user + ")" +
		"(A;" + flags + ";FA;;;SY)" +
		"(A;" + flags + ";FA;;;BA)")
}

func preparePrivateDirectory(dir string) error {
	fi, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		if err := createPrivateDirectory(dir); err != nil {
			return err
		}
		fi, err = os.Lstat(dir)
	}
	if err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return ErrUnsafePath
	}
	name, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return ErrUnsafePath
	}
	attrs, err := windows.GetFileAttributes(name)
	if err != nil || attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return ErrUnsafePath
	}
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return ErrUnsafePath
	}
	if err := checkPrivateDirectoryDescriptor(sd); err != nil {
		return ErrUnsafePath
	}
	return nil
}

func createPrivateDirectory(dir string) error {
	parent := filepath.Dir(dir)
	if parent == dir {
		return ErrUnsafePath
	}
	if _, err := os.Lstat(parent); os.IsNotExist(err) {
		if err := preparePrivateDirectory(parent); err != nil {
			return err
		}
	} else if err != nil {
		return ErrUnsafePath
	}
	// Unlike MkdirAll followed by SetSecurityInfo, passing the DACL at
	// creation never exposes a newly created directory with a public ACL.
	sd, err := privateDescriptor(true)
	if err != nil {
		return ErrUnsafePath
	}
	name, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return ErrUnsafePath
	}
	sa := windows.SecurityAttributes{
		Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd,
	}
	err = windows.CreateDirectory(name, &sa)
	runtime.KeepAlive(sd)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return ErrUnsafePath
	}
	return nil
}

func checkPrivateDirectoryDescriptor(sd *windows.SECURITY_DESCRIPTOR) error {
	if sd == nil {
		return ErrUnsafePath
	}
	user, err := currentUserSID()
	if err != nil {
		return ErrUnsafePath
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !trustedSID(owner.String(), user) {
		return ErrUnsafePath
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		// A missing/null DACL grants everyone access.
		return ErrUnsafePath
	}
	inheritsForUser := false
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil || ace == nil {
			return ErrUnsafePath
		}
		switch ace.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE:
			continue
		case windows.ACCESS_ALLOWED_ACE_TYPE:
		default:
			// Conditional/object ACEs need a richer evaluator. Never assume
			// an unknown allowed ACE is safe.
			return ErrUnsafePath
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()
		creatorOwner := sid == "S-1-3-0" && ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0
		if !trustedSID(sid, user) && !creatorOwner {
			// Check inherit-only ACEs too: they control the journal's ACL.
			return ErrUnsafePath
		}
		if (sid == user || creatorOwner) && ace.Header.AceFlags&windows.OBJECT_INHERIT_ACE != 0 {
			inheritsForUser = true
		}
	}
	if !inheritsForUser {
		// Without inheritable entries, Windows may use the creator token's
		// default DACL for a journal. Require explicit private inheritance.
		return ErrUnsafePath
	}
	return nil
}

func openPrivateFile(file string, flags int) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(file)
	if err != nil {
		return nil, err
	}
	disposition := uint32(windows.OPEN_EXISTING)
	var sa *windows.SecurityAttributes
	if flags&os.O_CREATE != 0 {
		disposition = windows.CREATE_NEW
		sd, err := privateDescriptor(false)
		if err != nil {
			return nil, err
		}
		sa = &windows.SecurityAttributes{
			Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd,
		}
	}
	// os.OpenFile does not request WRITE_DAC. Use a native handle so the
	// already-SameFile-verified handle can receive its private DACL.
	h, err := windows.CreateFile(name,
		windows.GENERIC_READ|windows.GENERIC_WRITE|windows.READ_CONTROL|windows.WRITE_DAC,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		sa, disposition, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	runtime.KeepAlive(sa)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), file), nil
}

func protectPrivateFile(f *os.File) error {
	h := windows.Handle(f.Fd())
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil ||
		info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		// Lstat does not necessarily model every reparse tag as a symlink.
		// SQLite subsequently reopens this path without OPEN_REPARSE_POINT,
		// so reject all reparse tags on the verified native file handle.
		return ErrUnsafePath
	}
	existing, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil || existing == nil {
		return ErrUnsafePath
	}
	user, err := currentUserSID()
	if err != nil {
		return ErrUnsafePath
	}
	owner, _, err := existing.Owner()
	if err != nil || owner == nil || !trustedSID(owner.String(), user) {
		return ErrUnsafePath
	}
	sd, err := privateDescriptor(false)
	if err != nil {
		return ErrUnsafePath
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return ErrUnsafePath
	}
	err = windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
	runtime.KeepAlive(sd)
	return err
}
