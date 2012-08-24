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

// Package schema manipulates Camlistore schema blobs.
//
// A schema blob is a JSON-encoded blob that describes other blobs.
// See documentation in Camlistore's doc/schema/ directory.
package schema

import (
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
	"sync"
	"time"
	"unicode/utf8"

	"camlistore.org/pkg/blobref"
)

// Map is an unencoded schema blob.
//
// A Map is typically used during construction of a new schema blob or
// claim.
type Map map[string]interface{}

// Type returns the map's "camliType" value.
func (m Map) Type() string {
	t, _ := m["camliType"].(string)
	return t
}

// SetClaimDate sets the "claimDate" on a claim.
// It is a fatal error to call SetClaimDate if the Map isn't of Type "claim".
func (m Map) SetClaimDate(t time.Time) {
	if t := m.Type(); t != "claim" {
		// This is a little gross, using panic here, but I
		// don't want all callers to check errors.  This is
		// really a programming error, not a runtime error
		// that would arise from e.g. random user data.
		panic("SetClaimDate called on non-claim Map; camliType=" + t)
	}
	m["claimDate"] = RFC3339FromTime(t)
}

var _ = log.Printf

var ErrNoCamliVersion = errors.New("schema: no camliVersion key in map")
var ErrUnimplemented = errors.New("schema: unimplemented")

type StatHasher interface {
	Lstat(fileName string) (os.FileInfo, error)
	Hash(fileName string) (*blobref.BlobRef, error)
}

// File is the interface returned when opening a DirectoryEntry that
// is a regular file.
type File interface {
	// TODO(bradfitz): this should instead be a ReaderAt with a Size() int64 method.
	// Then a Reader could be built with a SectionReader.

	Close() error
	Size() int64

	// Skip is an efficient way to skip n bytes into 
	Skip(n uint64) uint64

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

// Superset represents the superset of common Camlistore JSON schema
// keys as a convenient json.Unmarshal target.
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

	// Parts are references to the data chunks of a regular file (or a "bytes" schema blob).
	// See doc/schema/bytes.txt and doc/schema/files/file.txt.
	Parts []*BytesPart `json:"parts"`

	Entries string   `json:"entries"` // for directories, a blobref to a static-set
	Members []string `json:"members"` // for static sets (for directory static-sets:
	// blobrefs to child dirs/files)
}

// BytesPart is the type representing one of the "parts" in a "file"
// or "bytes" JSON schema.
//
// See doc/schema/bytes.txt and doc/schema/files/file.txt.
type BytesPart struct {
	// Size is the number of bytes that this part contributes to the overall segment.
	Size uint64 `json:"size"`

	// At most one of BlobRef or BytesRef must be set, but it's illegal for both to be set.
	// If neither are set, this BytesPart represents Size zero bytes.
	// BlobRef refers to raw bytes. BytesRef references a "bytes" schema blob.
	BlobRef  *blobref.BlobRef `json:"blobRef,omitempty"`
	BytesRef *blobref.BlobRef `json:"bytesRef,omitempty"`

	// Offset optionally specifies the offset into BlobRef to skip
	// when reading Size bytes.
	Offset uint64 `json:"offset,omitempty"`
}

// stringFromMixedArray joins a slice of either strings or float64
// values (as retrieved from JSON decoding) into a string.  These are
// used for non-UTF8 filenames in "fileNameBytes" fields.  The strings
// are UTF-8 segments and the float64s (actually uint8 values) are
// byte values.
func stringFromMixedArray(parts []interface{}) string {
	var buf bytes.Buffer
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

// MapUid returns the most appropriate mapping from this file's owner
// to the local machine's owner, trying first a match by name,
// followed by just mapping the number through directly.
func (ss *Superset) MapUid() int {
	if ss.UnixOwner != "" {
		uid, ok := getUidFromName(ss.UnixOwner)
		if ok {
			return uid
		}
	}
	return ss.UnixOwnerId // TODO: will be 0 if unset, which isn't ideal
}

func (ss *Superset) MapGid() int {
	if ss.UnixGroup != "" {
		gid, ok := getGidFromName(ss.UnixGroup)
		if ok {
			return gid
		}
	}
	return ss.UnixGroupId // TODO: will be 0 if unset, which isn't ideal
}

func (ss *Superset) ModTime() time.Time {
	if ss.UnixMtime == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, ss.UnixMtime)
	if err != nil {
		return time.Time{}
	}
	return t
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

func newMap(version int, ctype string) Map {
	return Map{
		"camliVersion": version,
		"camliType":    ctype,
	}
}

// NewUnsignedPermanode returns a new random permanode, not yet signed.
func NewUnsignedPermanode() Map {
	m := newMap(1, "permanode")
	chars := make([]byte, 20)
	_, err := io.ReadFull(rand.Reader, chars)
	if err != nil {
		panic("error reading random bytes: " + err.Error())
	}
	m["random"] = base64.StdEncoding.EncodeToString(chars)
	return m
}

// NewPlannedPermanode returns a permanode with a fixed key.  Like
// NewUnsignedPermanode, this Map is also not yet signed.  Callers of
// NewPlannedPermanode must sign the map with a fixed claimDate and
// GPG date to create consistent JSON encodings of the Map (its
// blobref), between runs.
func NewPlannedPermanode(key string) Map {
	m := newMap(1, "permanode")
	m["key"] = key
	return m
}

// Map returns a Camli map of camliType "static-set"
func (ss *StaticSet) Map() Map {
	m := newMap(1, "static-set")
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

// JSON returns the map m encoded as JSON in its
// recommended canonical form. The canonical form is readable with newlines and indentation,
// and always starts with the header bytes:
//
//   {"camliVersion":
//
func (m Map) JSON() (string, error) {
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
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "{\"camliVersion\": %v,\n", version)
	buf.Write(jsonBytes[2:])
	return buf.String(), nil
}

// NewFileMap returns a new Map of type "file" for the provided fileName.
// The chunk parts of the file are not populated.
func NewFileMap(fileName string) Map {
	m := newCommonFilenameMap(fileName)
	m["camliType"] = "file"
	return m
}

func newCommonFilenameMap(fileName string) Map {
	m := newMap(1, "" /* no type yet */)
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

var populateSchemaStat []func(schemaMap Map, fi os.FileInfo)

func NewCommonFileMap(fileName string, fi os.FileInfo) Map {
	m := newCommonFilenameMap(fileName)
	// Common elements (from file-common.txt)
	if fi.Mode()&os.ModeSymlink == 0 {
		m["unixPermission"] = fmt.Sprintf("0%o", fi.Mode().Perm())
	}

	// OS-specific population; defined in schema_posix.go, etc. (not on App Engine)
	for _, f := range populateSchemaStat {
		f(m, fi)
	}

	if mtime := fi.ModTime(); !mtime.IsZero() {
		m["unixMtime"] = RFC3339FromTime(mtime)
	}
	return m
}

func PopulateParts(m Map, size int64, parts []BytesPart) error {
	sumSize := int64(0)
	mparts := make([]Map, len(parts))
	for idx, part := range parts {
		mpart := make(Map)
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

// SetSymlinkTarget sets m to be of type "symlink" and sets the symlink's target.
func (m Map) SetSymlinkTarget(target string) {
	m["camliType"] = "symlink"
	if utf8.ValidString(target) {
		m["symlinkTarget"] = target
	} else {
		m["symlinkTargetBytes"] = []uint8(target)
	}
}

func newBytes() Map {
	return newMap(1, "bytes")
}

func PopulateDirectoryMap(m Map, staticSetRef *blobref.BlobRef) {
	m["camliType"] = "directory"
	m["entries"] = staticSetRef.String()
}

func NewShareRef(authType string, target *blobref.BlobRef, transitive bool) Map {
	m := newMap(1, "share")
	m["authType"] = authType
	m["target"] = target.String()
	m["transitive"] = transitive
	return m
}

const (
	SetAttribute = "set-attribute"
	AddAttribute = "add-attribute"
	DelAttribute = "del-attribute"
)

func newClaim(permaNode *blobref.BlobRef, t time.Time, claimType string) Map {
	m := newMap(1, "claim")
	m["permaNode"] = permaNode.String()
	m["claimType"] = claimType
	m.SetClaimDate(t)
	return m
}

func newAttrChangeClaim(permaNode *blobref.BlobRef, t time.Time, claimType, attr, value string) Map {
	m := newClaim(permaNode, t, claimType)
	m["attribute"] = attr
	m["value"] = value
	return m
}

func NewSetAttributeClaim(permaNode *blobref.BlobRef, attr, value string) Map {
	return newAttrChangeClaim(permaNode, time.Now(), SetAttribute, attr, value)
}

func NewAddAttributeClaim(permaNode *blobref.BlobRef, attr, value string) Map {
	return newAttrChangeClaim(permaNode, time.Now(), AddAttribute, attr, value)
}

func NewDelAttributeClaim(permaNode *blobref.BlobRef, attr string) Map {
	m := newAttrChangeClaim(permaNode, time.Now(), DelAttribute, attr, "")
	delete(m, "value")
	return m
}

// ShareHaveRef is the a share type specifying that if you "have the
// reference" (know the blobref to the haveref share blob), then you
// have access to the referenced object from that share blob.
// This is the "send a link to a friend" access model.
const ShareHaveRef = "haveref"

// RFC3339FromTime returns an RFC3339-formatted time in UTC.
// Fractional seconds are only included if the time has fractional
// seconds.
func RFC3339FromTime(t time.Time) string {
	if t.UnixNano()%1e9 == 0 {
		return t.UTC().Format(time.RFC3339)
	}
	return t.UTC().Format(time.RFC3339Nano)
}
