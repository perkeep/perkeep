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
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsDirectoryACLValidation(t *testing.T) {
	user, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	base := "O:" + user + "D:P(A;OICI;FA;;;" + user + ")"
	for _, tc := range []struct {
		name, sddl string
		valid      bool
	}{
		{"private", base + "(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)", true},
		{"creator owner", base + "(A;OICIIO;FA;;;CO)", true},
		{"deny other", "O:" + user + "D:P(D;;FA;;;WD)(A;OICI;FA;;;" + user + ")", true},
		{"public read", base + "(A;;FR;;;WD)", false},
		{"public inherited read", base + "(A;OIIO;FR;;;WD)", false},
		{"public write", base + "(A;;FW;;;BU)", false},
		{"untrusted owner", "O:WDD:P(A;OICI;FA;;;" + user + ")", false},
		{"no journal inheritance", "O:" + user + "D:P(A;;FA;;;" + user + ")", false},
		{"null dacl", "O:" + user + "D:NO_ACCESS_CONTROL", false},
		{"empty dacl", "O:" + user + "D:P", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sd, err := windows.SecurityDescriptorFromString(tc.sddl)
			if err != nil {
				t.Fatal(err)
			}
			err = checkPrivateDirectoryDescriptor(sd)
			if (err == nil) != tc.valid {
				t.Fatalf("descriptor validation = %v, want valid=%v", err, tc.valid)
			}
		})
	}
}

func windowsDescriptor(t *testing.T, path string) *windows.SECURITY_DESCRIPTOR {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil || sd == nil {
		t.Fatalf("read native security descriptor: %v", err)
	}
	return sd
}

func assertWindowsPrivateACL(t *testing.T, path string, protected bool) {
	t.Helper()
	sd := windowsDescriptor(t, path)
	user, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !trustedSID(owner.String(), user) {
		t.Fatal("untrusted file owner")
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil || dacl.AceCount == 0 {
		t.Fatal("missing private DACL")
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatal(err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			t.Fatal("unexpected ACE type")
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()
		if !trustedSID(sid, user) {
			t.Fatal("file DACL grants an untrusted principal access")
		}
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	if protected && control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("file DACL is not protected from inheritance")
	}
}

func setWindowsDACL(t *testing.T, path, sddl string) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
	runtime.KeepAlive(sd)
	if err != nil {
		t.Fatal(err)
	}
}

func TestWindowsPreparePrivateDirectory(t *testing.T) {
	for _, subdir := range []string{"private", filepath.Join("private", "nested")} {
		t.Run(subdir, func(t *testing.T) {
			base := t.TempDir()
			dir := filepath.Join(base, subdir)
			if err := preparePrivateDirectory(dir); err != nil {
				t.Fatalf("prepare missing directory: %v", err)
			}
			before := make(map[string]string)
			for path := dir; path != base; path = filepath.Dir(path) {
				assertWindowsPrivateACL(t, path, true)
				before[path] = windowsDescriptor(t, path).String()
			}
			if err := preparePrivateDirectory(dir); err != nil {
				t.Fatalf("prepare existing directory: %v", err)
			}
			for path, descriptor := range before {
				if after := windowsDescriptor(t, path).String(); after != descriptor {
					t.Fatal("preparing an existing directory changed its ACL")
				}
			}
		})
	}
}

func TestWindowsPrivateDatabaseAndJournal(t *testing.T) {
	dir := privateTempDir(t)
	before := windowsDescriptor(t, dir).String()
	file := filepath.Join(dir, "credentials.sqlite")
	s := diskStore(t, file)
	mustPut(t, s, "token", "private-value", PutOptions{})
	assertWindowsPrivateACL(t, file, true)
	if after := windowsDescriptor(t, dir).String(); after != before {
		t.Fatal("Open changed an existing directory ACL")
	}
	tx, err := s.db.BeginTx(background, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := writeRecord(background, tx, record{Entry: Entry{
		SecretMetadata: Metadata{Name: "other", Revision: 1, Updated: time.Now()},
		Value:          "other-private-value",
	}}); err != nil {
		t.Fatal(err)
	}
	// The journal is created by SQLite, not openPrivateFile: inheritance must
	// make it private from its first byte, even though it is not protected.
	assertWindowsPrivateACL(t, file+"-journal", false)
}

func TestWindowsRejectPublicParentWithoutChangingIt(t *testing.T) {
	dir := filepath.Join(privateTempDir(t), "public")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	user, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	setWindowsDACL(t, dir, "D:P(A;OICI;FA;;;"+user+")(A;OICI;FR;;;WD)")
	before := windowsDescriptor(t, dir).String()
	file := filepath.Join(dir, "credentials.sqlite")
	if _, err := Open(file); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("Open(public parent) = %v, want unsafe path", err)
	}
	if _, err := os.Lstat(file); !os.IsNotExist(err) {
		t.Fatal("created a database in a public directory")
	}
	if after := windowsDescriptor(t, dir).String(); after != before {
		t.Fatal("Open changed an unsafe existing directory ACL")
	}
}

func TestWindowsTightenExistingFileAndSidecar(t *testing.T) {
	dir := privateTempDir(t)
	user, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"credentials.sqlite", "credentials.sqlite-journal"} {
		file := filepath.Join(dir, name)
		if err := os.WriteFile(file, nil, 0600); err != nil {
			t.Fatal(err)
		}
		setWindowsDACL(t, file, "D:P(A;;FA;;;"+user+")(A;;FR;;;WD)")
		if err := privateFile(file, false); err != nil {
			t.Fatal(err)
		}
		assertWindowsPrivateACL(t, file, true)
	}
}

func TestWindowsRejectReparseFileHandle(t *testing.T) {
	dir := privateTempDir(t)
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		if errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD) || errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			t.Skip("Windows symlink creation privilege unavailable")
		}
		t.Fatal(err)
	}
	before := windowsDescriptor(t, target).String()
	if err := privateFile(link, false); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("privateFile(symlink) = %v, want unsafe path", err)
	}
	// Exercise the native handle check independently of Lstat's symlink
	// classification. The same check rejects non-symlink reparse tags.
	f, err := openPrivateFile(link, os.O_RDWR)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := protectPrivateFile(f); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("protectPrivateFile(reparse handle) = %v, want unsafe path", err)
	}
	if after := windowsDescriptor(t, target).String(); after != before {
		t.Fatal("reparse rejection changed the target ACL")
	}
}
