/*
Copyright 2011 Google Inc.

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

package schema

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"camlistore.org/pkg/blobref"
)

var _ = log.Printf

var ErrNoCamliVersion = errors.New("schema: no camliVersion key in map")
var ErrUnimplemented = errors.New("schema: unimplemented")

type StatHasher interface {
	Lstat(fileName string) (os.FileInfo, error)
	Hash(fileName string) (*blobref.BlobRef, error)
}

type File interface {
	Close() error
	Skip(skipBytes uint64) uint64
	Read(p []byte) (int, error)
}

// Directory is a read-only interface to a "directory" schema blob.
type Directory interface {
	// Readdir reads the contents of the directory associated with dr
	// and returns an array of up to n DirectoryEntries structures.
	// Subsequent calls on the same file will yield further
	// DirectoryEntries.
	// If n > 0, Readdir returns at most n DirectoryEntry structures. In
	// this case, if Readdir returns an empty slice, it will return
	// a non-nil error explaining why. At the end of a directory,
	// the error is os.EOF.
	// If n <= 0, Readdir returns all the DirectoryEntries from the
	// directory in a single slice. In this case, if Readdir succeeds
	// (reads all the way to the end of the directory), it returns the
	// slice and a nil os.Error. If it encounters an error before the
	// end of the directory, Readdir returns the DirectoryEntry read
	// until that point and a non-nil error.
	Readdir(count int) ([]DirectoryEntry, error)
}

type Symlink interface {
	// .. TODO
}

// DirectoryEntry is a read-only interface to an entry in a (static)
// directory.
type DirectoryEntry interface {
	// CamliType returns the schema blob's "camliType" field.
	// This may be "file", "directory", "symlink", or other more
	// obscure types added in the future.
	CamliType() string

	FileName() string
	BlobRef() *blobref.BlobRef

	File() (File, error)           // if camliType is "file"
	Directory() (Directory, error) // if camliType is "directory"
	Symlink() (Symlink, error)     // if camliType is "symlink"
}

// dirEntry is the default implementation of DirectoryEntry
type dirEntry struct {
	ss      Superset
	fetcher blobref.SeekFetcher
	fr      *FileReader // or nil if not a file
	dr      *DirReader  // or nil if not a directory
}

func (de *dirEntry) CamliType() string {
	return de.ss.Type
}

func (de *dirEntry) FileName() string {
	return de.ss.FileNameString()
}

func (de *dirEntry) BlobRef() *blobref.BlobRef {
	return de.ss.BlobRef
}

func (de *dirEntry) File() (File, error) {
	if de.fr == nil {
		if de.ss.Type != "file" {
			return nil, fmt.Errorf("DirectoryEntry is camliType %q, not %q", de.ss.Type, "file")
		}
		fr, err := NewFileReader(de.fetcher, de.ss.BlobRef)
		if err != nil {
			return nil, err
		}
		de.fr = fr
	}
	return de.fr, nil
}

func (de *dirEntry) Directory() (Directory, error) {
	if de.dr == nil {
		if de.ss.Type != "directory" {
			return nil, fmt.Errorf("DirectoryEntry is camliType %q, not %q", de.ss.Type, "directory")
		}
		dr, err := NewDirReader(de.fetcher, de.ss.BlobRef)
		if err != nil {
			return nil, err
		}
		de.dr = dr
	}
	return de.dr, nil
}

func (de *dirEntry) Symlink() (Symlink, error) {
	return 0, errors.New("TODO: Symlink not implemented")
}

// NewDirectoryEntry takes a Superset and returns a DirectoryEntry if
// the Supserset is valid and represents an entry in a directory.  It
// must by of type "file", "directory", or "symlink".
// TODO(mpl): symlink
// TODO: "fifo", "socket", "char", "block", probably.  later.
func NewDirectoryEntry(fetcher blobref.SeekFetcher, ss *Superset) (DirectoryEntry, error) {
	if ss == nil {
		return nil, errors.New("ss was nil")
	}
	if ss.BlobRef == nil {
		return nil, errors.New("ss.BlobRef was nil")
	}
	switch ss.Type {
	case "file", "directory", "symlink":
		// Okay
	default:
		return nil, fmt.Errorf("invalid DirectoryEntry camliType of %q", ss.Type)
	}
	de := &dirEntry{ss: *ss, fetcher: fetcher} // defensive copy
	return de, nil
}

// NewDirectoryEntryFromBlobRef takes a BlobRef and returns a
// DirectoryEntry if the BlobRef contains a type "file", "directory"
// or "symlink".
// TODO: "fifo", "socket", "char", "block", probably.  later.
func NewDirectoryEntryFromBlobRef(fetcher blobref.SeekFetcher, blobRef *blobref.BlobRef) (DirectoryEntry, error) {
	ss := new(Superset)
	err := ss.setFromBlobRef(fetcher, blobRef)
	if err != nil {
		return nil, fmt.Errorf("schema/filereader: can't fill Superset: %v\n", err)
	}
	return NewDirectoryEntry(fetcher, ss)
}

// Superset represents the superset of common camlistore JSON schema
// keys as a convenient json.Unmarshal target
type Superset struct {
	BlobRef *blobref.BlobRef // Not in JSON, but included for
	// those who want to set it.

	Version int    `json:"camliVersion"`
	Type    string `json:"camliType"`

	Signer string `json:"camliSigner"`
	Sig    string `json:"camliSig"`

	ClaimType string `json:"claimType"`
	ClaimDate string `json:"claimDate"`

	Permanode string `json:"permaNode"`
	Attribute string `json:"attribute"`
	Value     string `json:"value"`

	// TODO: ditch both the FooBytes variants below. a string doesn't have to be UTF-8.

	FileName      string        `json:"fileName"`
	FileNameBytes []interface{} `json:"fileNameBytes"` // TODO: needs custom UnmarshalJSON?

	SymlinkTarget      string        `json:"symlinkTarget"`
	SymlinkTargetBytes []interface{} `json:"symlinkTargetBytes"` // TODO: needs custom UnmarshalJSON?

	UnixPermission string `json:"unixPermission"`
	UnixOwnerId    int    `json:"unixOwnerId"`
	UnixOwner      string `json:"unixOwner"`
	UnixGroupId    int    `json:"unixGroupId"`
	UnixGroup      string `json:"unixGroup"`
	UnixMtime      string `json:"unixMtime"`
	UnixCtime      string `json:"unixCtime"`
	UnixAtime      string `json:"unixAtime"`

	Parts []*BytesPart `json:"parts"`

	Entries string   `json:"entries"` // for directories, a blobref to a static-set
	Members []string `json:"members"` // for static sets (for directory static-sets:
	// blobrefs to child dirs/files)
}

type BytesPart struct {
	// Required.
	Size uint64 `json:"size"`

	// At most one of:
	BlobRef  *blobref.BlobRef `json:"blobRef,omitempty"`
	BytesRef *blobref.BlobRef `json:"bytesRef,omitempty"`

	// Optional (default value is zero if unset anyway):
	Offset uint64 `json:"offset,omitempty"`
}

func stringFromMixedArray(parts []interface{}) string {
	buf := new(bytes.Buffer)
	for _, part := range parts {
		if s, ok := part.(string); ok {
			buf.WriteString(s)
			continue
		}
		if num, ok := part.(float64); ok {
			buf.WriteByte(byte(num))
			continue
		}
	}
	return buf.String()
}

func (ss *Superset) SumPartsSize() (size uint64) {
	for _, part := range ss.Parts {
		size += uint64(part.Size)
	}
	return size
}

func (ss *Superset) SymlinkTargetString() string {
	if ss.SymlinkTarget != "" {
		return ss.SymlinkTarget
	}
	return stringFromMixedArray(ss.SymlinkTargetBytes)
}

func (ss *Superset) FileNameString() string {
	if ss.FileName != "" {
		return ss.FileName
	}
	return stringFromMixedArray(ss.FileNameBytes)
}

func (ss *Superset) HasFilename(name string) bool {
	return ss.FileNameString() == name
}

func (ss *Superset) FileMode() os.FileMode {
	var mode os.FileMode
	m64, err := strconv.ParseUint(ss.UnixPermission, 8, 64)
	if err == nil {
		mode = mode | os.FileMode(m64)
	}

	// TODO: add other types (block, char, etc)
	switch ss.Type {
	case "directory":
		mode = mode | os.ModeDir
	case "file":
		// No extra bit.
	case "symlink":
		mode = mode | os.ModeSymlink
	}
	return mode
}

var DefaultStatHasher = &defaultStatHasher{}

type defaultStatHasher struct{}

func (d *defaultStatHasher) Lstat(fileName string) (os.FileInfo, error) {
	return os.Lstat(fileName)
}

func (d *defaultStatHasher) Hash(fileName string) (*blobref.BlobRef, error) {
	s1 := sha1.New()
	file, err := os.Open(fileName)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	_, err = io.Copy(s1, file)
	if err != nil {
		return nil, err
	}
	return blobref.FromHash("sha1", s1), nil
}

type StaticSet struct {
	l    sync.Mutex
	refs []*blobref.BlobRef
}

func (ss *StaticSet) Add(ref *blobref.BlobRef) {
	ss.l.Lock()
	defer ss.l.Unlock()
	ss.refs = append(ss.refs, ref)
}

func newCamliMap(version int, ctype string) map[string]interface{} {
	m := make(map[string]interface{})
	m["camliVersion"] = version
	m["camliType"] = ctype
	return m
}

func NewUnsignedPermanode() map[string]interface{} {
	m := newCamliMap(1, "permanode")
	chars := make([]byte, 20)
	_, err := io.ReadFull(rand.Reader, chars)
	if err != nil {
		panic("error reading random bytes: " + err.Error())
	}
	m["random"] = base64.StdEncoding.EncodeToString(chars)
	return m
}

// Map returns a Camli map of camliType "static-set"
func (ss *StaticSet) Map() map[string]interface{} {
	m := newCamliMap(1, "static-set")
	ss.l.Lock()
	defer ss.l.Unlock()

	members := make([]string, 0, len(ss.refs))
	if ss.refs != nil {
		for _, ref := range ss.refs {
			members = append(members, ref.String())
		}
	}
	m["members"] = members
	return m
}

func MapToCamliJson(m map[string]interface{}) (string, error) {
	version, hasVersion := m["camliVersion"]
	if !hasVersion {
		return "", ErrNoCamliVersion
	}
	delete(m, "camliVersion")
	jsonBytes, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", err
	}
	m["camliVersion"] = version
	buf := new(bytes.Buffer)
	fmt.Fprintf(buf, "{\"camliVersion\": %v,\n", version)
	buf.Write(jsonBytes[2:])
	return string(buf.Bytes()), nil
}

func NewFileMap(fileName string) map[string]interface{} {
	m := NewCommonFilenameMap(fileName)
	m["camliType"] = "file"
	return m
}

func NewCommonFilenameMap(fileName string) map[string]interface{} {
	m := newCamliMap(1, "" /* no type yet */)
	if fileName != "" {
		baseName := filepath.Base(fileName)
		if utf8.ValidString(baseName) {
			m["fileName"] = baseName
		} else {
			m["fileNameBytes"] = []uint8(baseName)
		}
	}
	return m
}

var populateSchemaStat []func(schemaMap map[string]interface{}, fi os.FileInfo)

func NewCommonFileMap(fileName string, fi os.FileInfo) map[string]interface{} {
	m := NewCommonFilenameMap(fileName)
	// Common elements (from file-common.txt)
	if fi.Mode()&os.ModeSymlink != 0 {
		m["unixPermission"] = fmt.Sprintf("0%o", fi.Mode().Perm())
	}

	// OS-specific population; defined in schema_posix.go, etc. (not on App Engine)
	for _, f := range populateSchemaStat {
		f(m, fi)
	}

	/**
	 TODO-GO1(bradfitz): this will need to do fi.Sys().(*syscall....), but
	 syscall can't run on App Engine, so will need build context tags for
	 safe/unsafe and seperate helper files?

	if fi.Uid != -1 {
		m["unixOwnerId"] = fi.Uid
		if user := getUserFromUid(fi.Uid); user != "" {
			m["unixOwner"] = user
		}
	}
	if fi.Gid != -1 {
		m["unixGroupId"] = fi.Gid
		if group := getGroupFromGid(fi.Gid); group != "" {
			m["unixGroup"] = group
		}
	}
	// Include the ctime too, if it differs.
	if ctime := fi.Ctime_ns; ctime != 0 && fi.ModTime() != fi.Ctime_ns {
		m["unixCtime"] = RFC3339FromTime(ctime)
	}
	*/
	if mtime := fi.ModTime(); !mtime.IsZero() {
		m["unixMtime"] = RFC3339FromTime(mtime)
	}
	return m
}

func PopulateParts(m map[string]interface{}, size int64, parts []BytesPart) error {
	sumSize := int64(0)
	mparts := make([]map[string]interface{}, len(parts))
	for idx, part := range parts {
		mpart := make(map[string]interface{})
		mparts[idx] = mpart
		switch {
		case part.BlobRef != nil && part.BytesRef != nil:
			return errors.New("schema: part contains both blobRef and bytesRef")
		case part.BlobRef != nil:
			mpart["blobRef"] = part.BlobRef.String()
		case part.BytesRef != nil:
			mpart["bytesRef"] = part.BytesRef.String()
		}
		mpart["size"] = part.Size
		sumSize += int64(part.Size)
		if part.Offset != 0 {
			mpart["offset"] = part.Offset
		}
	}
	if sumSize != size {
		return fmt.Errorf("schema: declared size %d doesn't match sum of parts size %d", size, sumSize)
	}
	m["parts"] = mparts
	return nil
}

func PopulateSymlinkMap(m map[string]interface{}, fileName string) error {
	m["camliType"] = "symlink"
	target, err := os.Readlink(fileName)
	if err != nil {
		return err
	}
	if utf8.ValidString(target) {
		m["symlinkTarget"] = target
	} else {
		m["symlinkTargetBytes"] = []uint8(target)
	}
	return nil
}

func NewBytes() map[string]interface{} {
	return newCamliMap(1, "bytes")
}

func PopulateDirectoryMap(m map[string]interface{}, staticSetRef *blobref.BlobRef) {
	m["camliType"] = "directory"
	m["entries"] = staticSetRef.String()
}

func NewShareRef(authType string, target *blobref.BlobRef, transitive bool) map[string]interface{} {
	m := newCamliMap(1, "share")
	m["authType"] = authType
	m["target"] = target.String()
	m["transitive"] = transitive
	return m
}

func NewClaim(permaNode *blobref.BlobRef, claimType string) map[string]interface{} {
	m := newCamliMap(1, "claim")
	m["permaNode"] = permaNode.String()
	m["claimType"] = claimType
	m["claimDate"] = RFC3339FromTime(time.Now())
	return m
}

func newAttrChangeClaim(permaNode *blobref.BlobRef, claimType, attr, value string) map[string]interface{} {
	m := NewClaim(permaNode, claimType)
	m["attribute"] = attr
	m["value"] = value
	return m
}

func NewSetAttributeClaim(permaNode *blobref.BlobRef, attr, value string) map[string]interface{} {
	return newAttrChangeClaim(permaNode, "set-attribute", attr, value)
}

func NewAddAttributeClaim(permaNode *blobref.BlobRef, attr, value string) map[string]interface{} {
	return newAttrChangeClaim(permaNode, "add-attribute", attr, value)
}

func NewDelAttributeClaim(permaNode *blobref.BlobRef, attr string) map[string]interface{} {
	m := newAttrChangeClaim(permaNode, "del-attribute", attr, "")
	delete(m, "value")
	return m
}

// Types of ShareRefs
const ShareHaveRef = "haveref"

func RFC3339FromTime(t time.Time) string {
	// TODO-GO1: this is now needless complex after the gofix
	// and signature change.
	epochnanos := t.UnixNano()
	nanos := epochnanos % 1e9
	esec := epochnanos / 1e9
	t = time.Unix(esec, 0).UTC()
	timeStr := t.Format(time.RFC3339)
	if nanos == 0 {
		return timeStr
	}
	nanoStr := fmt.Sprintf("%09d", nanos)
	nanoStr = strings.TrimRight(nanoStr, "0")
	return timeStr[:len(timeStr)-1] + "." + nanoStr + "Z"
}

func NanosFromRFC3339(timestr string) int64 {
	dotpos := strings.Index(timestr, ".")
	simple3339 := timestr
	nanostr := ""
	if dotpos != -1 {
		if !strings.HasSuffix(timestr, "Z") {
			return -1
		}
		simple3339 = timestr[:dotpos] + "Z"
		nanostr = timestr[dotpos+1 : len(timestr)-1]
		if needDigits := 9 - len(nanostr); needDigits > 0 {
			nanostr = nanostr + "000000000"[:needDigits]
		}
	}
	t, err := time.Parse(time.RFC3339, simple3339)
	if err != nil {
		return -1
	}
	nanos, _ := strconv.ParseInt(nanostr, 10, 64)
	return t.Unix()*1e9 + nanos
}

func populateMap(m map[int]string, file string) {
	f, err := os.Open(file)
	if err != nil {
		return
	}
	bufr := bufio.NewReader(f)
	for {
		line, err := bufr.ReadString('\n')
		if err != nil {
			return
		}
		parts := strings.SplitN(line, ":", 4)
		if len(parts) >= 3 {
			idstr := parts[2]
			id, err := strconv.Atoi(idstr)
			if err == nil {
				m[id] = parts[0]
			}
		}
	}
}

var uidToUsernameMap map[int]string
var getUserFromUidOnce sync.Once

func getUserFromUid(uid int) string {
	getUserFromUidOnce.Do(func() {
		uidToUsernameMap = make(map[int]string)
		populateMap(uidToUsernameMap, "/etc/passwd")
	})
	return uidToUsernameMap[uid]
}

var gidToUsernameMap map[int]string
var getGroupFromGidOnce sync.Once

func getGroupFromGid(uid int) string {
	getGroupFromGidOnce.Do(func() {
		gidToUsernameMap = make(map[int]string)
		populateMap(gidToUsernameMap, "/etc/group")
	})
	return gidToUsernameMap[uid]
}
