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
	"hash"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/types"
	"camlistore.org/third_party/github.com/camlistore/goexif/exif"
)

// MaxSchemaBlobSize represents the upper bound for how large
// a schema blob may be.
const MaxSchemaBlobSize = 1 << 20

var sha1Type = reflect.TypeOf(sha1.New())

var (
	ErrNoCamliVersion = errors.New("schema: no camliVersion key in map")
)

var clockNow = time.Now

type StatHasher interface {
	Lstat(fileName string) (os.FileInfo, error)
	Hash(fileName string) (blob.Ref, error)
}

// File is the interface returned when opening a DirectoryEntry that
// is a regular file.
type File interface {
	io.Closer
	io.ReaderAt
	io.Reader
	Size() int64
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
	BlobRef() blob.Ref

	File() (File, error)           // if camliType is "file"
	Directory() (Directory, error) // if camliType is "directory"
	Symlink() (Symlink, error)     // if camliType is "symlink"
}

// dirEntry is the default implementation of DirectoryEntry
type dirEntry struct {
	ss      superset
	fetcher blob.Fetcher
	fr      *FileReader // or nil if not a file
	dr      *DirReader  // or nil if not a directory
}

func (de *dirEntry) CamliType() string {
	return de.ss.Type
}

func (de *dirEntry) FileName() string {
	return de.ss.FileNameString()
}

func (de *dirEntry) BlobRef() blob.Ref {
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

// newDirectoryEntry takes a superset and returns a DirectoryEntry if
// the Supserset is valid and represents an entry in a directory.  It
// must by of type "file", "directory", or "symlink".
// TODO: "fifo", "socket", "char", "block", probably.  later.
func newDirectoryEntry(fetcher blob.Fetcher, ss *superset) (DirectoryEntry, error) {
	if ss == nil {
		return nil, errors.New("ss was nil")
	}
	if !ss.BlobRef.Valid() {
		return nil, errors.New("ss.BlobRef was invalid")
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
func NewDirectoryEntryFromBlobRef(fetcher blob.Fetcher, blobRef blob.Ref) (DirectoryEntry, error) {
	ss := new(superset)
	err := ss.setFromBlobRef(fetcher, blobRef)
	if err != nil {
		return nil, fmt.Errorf("schema/filereader: can't fill superset: %v\n", err)
	}
	return newDirectoryEntry(fetcher, ss)
}

// superset represents the superset of common Camlistore JSON schema
// keys as a convenient json.Unmarshal target.
// TODO(bradfitz): unexport this type. Getting too gross. Move to schema.Blob
type superset struct {
	// BlobRef isn't for a particular metadata blob field, but included
	// for convenience.
	BlobRef blob.Ref

	Version int    `json:"camliVersion"`
	Type    string `json:"camliType"`

	Signer blob.Ref `json:"camliSigner"`
	Sig    string   `json:"camliSig"`

	ClaimType string         `json:"claimType"`
	ClaimDate types.Time3339 `json:"claimDate"`

	Permanode blob.Ref `json:"permaNode"`
	Attribute string   `json:"attribute"`
	Value     string   `json:"value"`

	// FileName and FileNameBytes represent one of the two
	// representations of file names in schema blobs.  They should
	// not be accessed directly.  Use the FileNameString accessor
	// instead, which also sanitizes malicious values.
	FileName      string        `json:"fileName"`
	FileNameBytes []interface{} `json:"fileNameBytes"`

	SymlinkTarget      string        `json:"symlinkTarget"`
	SymlinkTargetBytes []interface{} `json:"symlinkTargetBytes"`

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

	Entries blob.Ref   `json:"entries"` // for directories, a blobref to a static-set
	Members []blob.Ref `json:"members"` // for static sets (for directory static-sets: blobrefs to child dirs/files)

	// Target is a "share" blob's target (the thing being shared)
	// Or it is the object being deleted in a DeleteClaim claim.
	Target blob.Ref `json:"target"`
	// Transitive is a property of a "share" blob.
	Transitive bool `json:"transitive"`
	// AuthType is a "share" blob's authentication type that is required.
	// Currently (2013-01-02) just "haveref" (if you know the share's blobref,
	// you get access: the secret URL model)
	AuthType string         `json:"authType"`
	Expires  types.Time3339 `json:"expires"` // or zero for no expiration
}

func parseSuperset(r io.Reader) (*superset, error) {
	var ss superset
	if err := json.NewDecoder(io.LimitReader(r, 1<<20)).Decode(&ss); err != nil {
		return nil, err
	}
	return &ss, nil
}

// BlobReader returns a new Blob from the provided Reader r,
// which should be the body of the provided blobref.
// Note: the hash checksum is not verified.
func BlobFromReader(ref blob.Ref, r io.Reader) (*Blob, error) {
	if !ref.Valid() {
		return nil, errors.New("schema.BlobFromReader: invalid blobref")
	}
	var buf bytes.Buffer
	tee := io.TeeReader(r, &buf)
	ss, err := parseSuperset(tee)
	if err != nil {
		return nil, err
	}
	var wb [16]byte
	afterObj := 0
	for {
		n, err := tee.Read(wb[:])
		afterObj += n
		for i := 0; i < n; i++ {
			if !isASCIIWhite(wb[i]) {
				return nil, fmt.Errorf("invalid bytes after JSON schema blob in %v", ref)
			}
		}
		if afterObj > MaxSchemaBlobSize {
			break
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	json := buf.String()
	if len(json) > MaxSchemaBlobSize {
		return nil, fmt.Errorf("schema: metadata blob %v is over expected limit; size=%d", ref, len(json))
	}
	return &Blob{ref, json, ss}, nil
}

func isASCIIWhite(b byte) bool {
	switch b {
	case ' ', '\t', '\r', '\n':
		return true
	}
	return false
}

// BytesPart is the type representing one of the "parts" in a "file"
// or "bytes" JSON schema.
//
// See doc/schema/bytes.txt and doc/schema/files/file.txt.
type BytesPart struct {
	// Size is the number of bytes that this part contributes to the overall segment.
	Size uint64 `json:"size"`

	// At most one of BlobRef or BytesRef must be non-zero
	// (Valid), but it's illegal for both.
	// If neither are set, this BytesPart represents Size zero bytes.
	// BlobRef refers to raw bytes. BytesRef references a "bytes" schema blob.
	BlobRef  blob.Ref `json:"blobRef,omitempty"`
	BytesRef blob.Ref `json:"bytesRef,omitempty"`

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

func mixedArrayFromString(s string) []interface{} {
	buf := []byte(s)
	var name []interface{}
	n := 0
	for n < len(buf) {
		part, offset := nextStringOrByte(buf[n:])
		name = append(name, part)
		n += offset
	}

	return name
}

func nextStringOrByte(b []byte) (interface{}, int) {
	n := 0
	var s []byte
	for n < len(b) {
		r, size := utf8.DecodeRune(b[n:])
		if r == utf8.RuneError {
			// If we already have a UTF8 string segment, return it
			if len(s) > 0 {
				return string(s), n
			}
			// Return the single byte and an offset of 1
			return b[n], 1
		}
		n += size // We have consumed size bytes
		c := make([]byte, utf8.RuneLen(r))
		_ = utf8.EncodeRune(c, r)
		s = append(s, c...)
	}

	return string(s), n
}

func (ss *superset) SumPartsSize() (size uint64) {
	for _, part := range ss.Parts {
		size += uint64(part.Size)
	}
	return size
}

func (ss *superset) SymlinkTargetString() string {
	if ss.SymlinkTarget != "" {
		return ss.SymlinkTarget
	}
	return stringFromMixedArray(ss.SymlinkTargetBytes)
}

// FileNameString returns the schema blob's base filename.
//
// If the fileName field of the blob accidentally or maliciously
// contains a slash, this function returns an empty string instead.
func (ss *superset) FileNameString() string {
	v := ss.FileName
	if v == "" {
		v = stringFromMixedArray(ss.FileNameBytes)
	}
	if v != "" {
		if strings.Index(v, "/") != -1 {
			// Bogus schema blob; ignore.
			return ""
		}
		if strings.Index(v, "\\") != -1 {
			// Bogus schema blob; ignore.
			return ""
		}
	}
	return v
}

func (ss *superset) HasFilename(name string) bool {
	return ss.FileNameString() == name
}

func (b *Blob) FileMode() os.FileMode {
	// TODO: move this to a different type, off *Blob
	return b.ss.FileMode()
}

func (ss *superset) FileMode() os.FileMode {
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
func (b *Blob) MapUid() int { return b.ss.MapUid() }

// MapGid returns the most appropriate mapping from this file's group
// to the local machine's group, trying first a match by name,
// followed by just mapping the number through directly.
func (b *Blob) MapGid() int { return b.ss.MapGid() }

func (ss *superset) MapUid() int {
	if ss.UnixOwner != "" {
		uid, ok := getUidFromName(ss.UnixOwner)
		if ok {
			return uid
		}
	}
	return ss.UnixOwnerId // TODO: will be 0 if unset, which isn't ideal
}

func (ss *superset) MapGid() int {
	if ss.UnixGroup != "" {
		gid, ok := getGidFromName(ss.UnixGroup)
		if ok {
			return gid
		}
	}
	return ss.UnixGroupId // TODO: will be 0 if unset, which isn't ideal
}

func (ss *superset) ModTime() time.Time {
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

func (d *defaultStatHasher) Hash(fileName string) (blob.Ref, error) {
	s1 := sha1.New()
	file, err := os.Open(fileName)
	if err != nil {
		return blob.Ref{}, err
	}
	defer file.Close()
	_, err = io.Copy(s1, file)
	if err != nil {
		return blob.Ref{}, err
	}
	return blob.RefFromHash(s1), nil
}

type StaticSet struct {
	l    sync.Mutex
	refs []blob.Ref
}

func (ss *StaticSet) Add(ref blob.Ref) {
	ss.l.Lock()
	defer ss.l.Unlock()
	ss.refs = append(ss.refs, ref)
}

func base(version int, ctype string) *Builder {
	return &Builder{map[string]interface{}{
		"camliVersion": version,
		"camliType":    ctype,
	}}
}

// NewUnsignedPermanode returns a new random permanode, not yet signed.
func NewUnsignedPermanode() *Builder {
	bb := base(1, "permanode")
	chars := make([]byte, 20)
	_, err := io.ReadFull(rand.Reader, chars)
	if err != nil {
		panic("error reading random bytes: " + err.Error())
	}
	bb.m["random"] = base64.StdEncoding.EncodeToString(chars)
	return bb
}

// NewPlannedPermanode returns a permanode with a fixed key.  Like
// NewUnsignedPermanode, this builder is also not yet signed.  Callers of
// NewPlannedPermanode must sign the map with a fixed claimDate and
// GPG date to create consistent JSON encodings of the Map (its
// blobref), between runs.
func NewPlannedPermanode(key string) *Builder {
	bb := base(1, "permanode")
	bb.m["key"] = key
	return bb
}

// NewHashPlannedPermanode returns a planned permanode with the sum
// of the hash, prefixed with "sha1-", as the key.
func NewHashPlannedPermanode(h hash.Hash) *Builder {
	if reflect.TypeOf(h) != sha1Type {
		panic("Hash not supported. Only sha1 for now.")
	}
	return NewPlannedPermanode(fmt.Sprintf("sha1-%x", h.Sum(nil)))
}

// Map returns a Camli map of camliType "static-set"
// TODO: delete this method
func (ss *StaticSet) Blob() *Blob {
	bb := base(1, "static-set")
	ss.l.Lock()
	defer ss.l.Unlock()

	members := make([]string, 0, len(ss.refs))
	if ss.refs != nil {
		for _, ref := range ss.refs {
			members = append(members, ref.String())
		}
	}
	bb.m["members"] = members
	return bb.Blob()
}

// JSON returns the map m encoded as JSON in its
// recommended canonical form. The canonical form is readable with newlines and indentation,
// and always starts with the header bytes:
//
//   {"camliVersion":
//
func mapJSON(m map[string]interface{}) (string, error) {
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

// NewFileMap returns a new builder of a type "file" schema for the provided fileName.
// The chunk parts of the file are not populated.
func NewFileMap(fileName string) *Builder {
	return newCommonFilenameMap(fileName).SetType("file")
}

// NewDirMap returns a new builder of a type "directory" schema for the provided fileName.
func NewDirMap(fileName string) *Builder {
	return newCommonFilenameMap(fileName).SetType("directory")
}

func newCommonFilenameMap(fileName string) *Builder {
	bb := base(1, "" /* no type yet */)
	if fileName != "" {
		bb.SetFileName(fileName)
	}
	return bb
}

var populateSchemaStat []func(schemaMap map[string]interface{}, fi os.FileInfo)

func NewCommonFileMap(fileName string, fi os.FileInfo) *Builder {
	bb := newCommonFilenameMap(fileName)
	// Common elements (from file-common.txt)
	if fi.Mode()&os.ModeSymlink == 0 {
		bb.m["unixPermission"] = fmt.Sprintf("0%o", fi.Mode().Perm())
	}

	// OS-specific population; defined in schema_posix.go, etc. (not on App Engine)
	for _, f := range populateSchemaStat {
		f(bb.m, fi)
	}

	if mtime := fi.ModTime(); !mtime.IsZero() {
		bb.m["unixMtime"] = RFC3339FromTime(mtime)
	}
	return bb
}

// PopulateParts sets the "parts" field of the blob with the provided
// parts.  The sum of the sizes of parts must match the provided size
// or an error is returned.  Also, each BytesPart may only contain either
// a BytesPart or a BlobRef, but not both.
func (bb *Builder) PopulateParts(size int64, parts []BytesPart) error {
	return populateParts(bb.m, size, parts)
}

func populateParts(m map[string]interface{}, size int64, parts []BytesPart) error {
	sumSize := int64(0)
	mparts := make([]map[string]interface{}, len(parts))
	for idx, part := range parts {
		mpart := make(map[string]interface{})
		mparts[idx] = mpart
		switch {
		case part.BlobRef.Valid() && part.BytesRef.Valid():
			return errors.New("schema: part contains both BlobRef and BytesRef")
		case part.BlobRef.Valid():
			mpart["blobRef"] = part.BlobRef.String()
		case part.BytesRef.Valid():
			mpart["bytesRef"] = part.BytesRef.String()
		default:
			return errors.New("schema: part must contain either a BlobRef or BytesRef")
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

func newBytes() *Builder {
	return base(1, "bytes")
}

// ClaimType is one of the valid "claimType" fields in a "claim" schema blob. See doc/schema/claims/.
type ClaimType string

const (
	SetAttributeClaim ClaimType = "set-attribute"
	AddAttributeClaim ClaimType = "add-attribute"
	DelAttributeClaim ClaimType = "del-attribute"
	ShareClaim        ClaimType = "share"
	// DeleteClaim deletes a permanode or another claim.
	// A delete claim can itself be deleted, and so on.
	DeleteClaim ClaimType = "delete"
)

// claimParam is used to populate a claim map when building a new claim
type claimParam struct {
	claimType ClaimType

	// Params specific to *Attribute claims:
	permanode blob.Ref // modified permanode
	attribute string   // required
	value     string   // optional if Type == DelAttributeClaim

	// Params specific to ShareClaim claims:
	authType     string
	transitive   bool
	shareExpires time.Time // Zero means no expiration

	// Params specific to ShareClaim and DeleteClaim claims.
	target blob.Ref
}

func newClaim(claims ...*claimParam) *Builder {
	bb := base(1, "claim")
	bb.SetClaimDate(clockNow())
	if len(claims) == 1 {
		cp := claims[0]
		populateClaimMap(bb.m, cp)
		return bb
	}
	var claimList []interface{}
	for _, cp := range claims {
		m := map[string]interface{}{}
		populateClaimMap(m, cp)
		claimList = append(claimList, m)
	}
	bb.m["claimType"] = "multi"
	bb.m["claims"] = claimList
	return bb
}

func populateClaimMap(m map[string]interface{}, cp *claimParam) {
	m["claimType"] = string(cp.claimType)
	switch cp.claimType {
	case ShareClaim:
		m["authType"] = cp.authType
		m["target"] = cp.target.String()
		m["transitive"] = cp.transitive
	case DeleteClaim:
		m["target"] = cp.target.String()
	default:
		m["permaNode"] = cp.permanode.String()
		m["attribute"] = cp.attribute
		if !(cp.claimType == DelAttributeClaim && cp.value == "") {
			m["value"] = cp.value
		}
	}
}

// NewShareRef creates a *Builder for a "share" claim.
func NewShareRef(authType string, target blob.Ref, transitive bool) *Builder {
	return newClaim(&claimParam{
		claimType:  ShareClaim,
		authType:   authType,
		target:     target,
		transitive: transitive,
	})
}

func NewSetAttributeClaim(permaNode blob.Ref, attr, value string) *Builder {
	return newClaim(&claimParam{
		permanode: permaNode,
		claimType: SetAttributeClaim,
		attribute: attr,
		value:     value,
	})
}

func NewAddAttributeClaim(permaNode blob.Ref, attr, value string) *Builder {
	return newClaim(&claimParam{
		permanode: permaNode,
		claimType: AddAttributeClaim,
		attribute: attr,
		value:     value,
	})
}

// NewDelAttributeClaim creates a new claim to remove value from the
// values set for the attribute attr of permaNode. If value is empty then
// all the values for attribute are cleared.
func NewDelAttributeClaim(permaNode blob.Ref, attr, value string) *Builder {
	return newClaim(&claimParam{
		permanode: permaNode,
		claimType: DelAttributeClaim,
		attribute: attr,
		value:     value,
	})
}

// NewDeleteClaim creates a new claim to delete a target claim or permanode.
func NewDeleteClaim(target blob.Ref) *Builder {
	return newClaim(&claimParam{
		target:    target,
		claimType: DeleteClaim,
	})
}

// ShareHaveRef is the auth type specifying that if you "have the
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

var bytesCamliVersion = []byte("camliVersion")

// LikelySchemaBlob returns quickly whether buf likely contains (or is
// the prefix of) a schema blob.
func LikelySchemaBlob(buf []byte) bool {
	if len(buf) == 0 || buf[0] != '{' {
		return false
	}
	return bytes.Contains(buf, bytesCamliVersion)
}

// findSize checks if v is an *os.File or if it has
// a Size() int64 method, to find its size.
// It returns 0, false otherwise.
func findSize(v interface{}) (size int64, ok bool) {
	if fi, ok := v.(*os.File); ok {
		v, _ = fi.Stat()
	}
	if sz, ok := v.(interface {
		Size() int64
	}); ok {
		return sz.Size(), true
	}
	// For bytes.Reader, strings.Reader, etc:
	if li, ok := v.(interface {
		Len() int
	}); ok {
		ln := int64(li.Len()) // unread portion, typically
		// If it's also a seeker, remove add any seek offset:
		if sk, ok := v.(io.Seeker); ok {
			if cur, err := sk.Seek(0, 1); err == nil {
				ln += cur
			}
		}
		return ln, true
	}
	return 0, false
}

// FileTime returns the best guess of the file's creation time (or modtime).
// If the file doesn't have its own metadata indication the creation time (such as in EXIF),
// FileTime uses the modification time from the file system.
// It there was a valid EXIF but an error while trying to get a date from it,
// it logs the error and tries the other methods.
func FileTime(f io.ReaderAt) (time.Time, error) {
	var ct time.Time
	defaultTime := func() (time.Time, error) {
		if osf, ok := f.(*os.File); ok {
			fi, err := osf.Stat()
			if err != nil {
				return ct, fmt.Errorf("Failed to find a modtime: lstat: %v", err)
			}
			return fi.ModTime(), nil
		}
		return ct, errors.New("All methods failed to find a creation time or modtime.")
	}

	size, ok := findSize(f)
	if !ok {
		size = 256 << 10 // enough to get the EXIF
	}
	r := io.NewSectionReader(f, 0, size)
	ex, err := exif.Decode(r)
	if err != nil {
		return defaultTime()
	}
	ct, err = ex.DateTime()
	if err != nil {
		return defaultTime()
	}
	return ct, nil
}
