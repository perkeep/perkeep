package schema

import (
	"bytes"
	"camli/blobref"
	"crypto/sha1"
	"fmt"
	"io"
	"json"
	"os"
	"strings"
	"time"
)

var NoCamliVersionError = os.NewError("No camliVersion key in map")
var UnimplementedError = os.NewError("Unimplemented")

type StatHasher interface {
	Lstat(fileName string) (*os.FileInfo, os.Error)
	Hash(fileName string) (*blobref.BlobRef, os.Error)
}

var DefaultStatHasher = &defaultStatHasher{}

type defaultStatHasher struct{}

func (d *defaultStatHasher) Lstat(fileName string) (*os.FileInfo, os.Error) {
	return os.Lstat(fileName)
}

func (d *defaultStatHasher) Hash(fileName string) (*blobref.BlobRef, os.Error) {
	s1 := sha1.New()
	file, err := os.Open(fileName, os.O_RDONLY, 0)
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

func MapToCamliJson(m map[string]interface{}) (string, os.Error) {
	version, hasVersion := m["camliVersion"]
	if !hasVersion {
		return "", NoCamliVersionError
	}
	m["camliVersion"] = 0, false
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

func newMapForFileName(fileName string) map[string]interface{} {
	m := make(map[string]interface{})
	m["camliVersion"] = 1
	m["camliType"] = "" // undefined at this point
	
	lastSlash := strings.LastIndex(fileName, "/")
	baseName := fileName[lastSlash+1:]
	if isValidUtf8(baseName) {
		m["fileName"] = baseName
	} else {
		m["fileNameBytes"] = []uint8(baseName)
	}
	return m
}

func populateRegularFileMap(m map[string]interface{}, fileName string, fi *os.FileInfo, sh StatHasher) os.Error {
	m["camliType"] = "file"
	m["size"] = fi.Size

	// Build the contentParts, currently just in one big chunk.
	// TODO: split mutatalbe metadata (EXIF, etc) from the payload
	// bytes
	blobref, err := sh.Hash(fileName)
	if err != nil {
		return err
	}
	parts := make([]map[string]interface{}, 0)
	solePart := make(map[string]interface{})
	solePart["blobRef"] = blobref.String()
	solePart["size"] = fi.Size
	parts = append(parts, solePart)
	m["contentParts"] = parts
	
	return nil
}

func NewFileMap(fileName string, sh StatHasher) (map[string]interface{}, os.Error) {
	if sh == nil {
		sh = DefaultStatHasher
	}
	m := newMapForFileName(fileName)
	fi, err := sh.Lstat(fileName)
	if err != nil {
		return nil, err
	}

	// Common elements (from file-common.txt)
	m["unixPermission"] = fmt.Sprintf("0%o", fi.Permission())
	if fi.Uid != -1 {
		m["unixOwnerId"] = fi.Uid
	}
	if fi.Gid != -1 {
		m["unixGroupId"] = fi.Gid
	}
	if mtime := fi.Mtime_ns; mtime != 0 {
		nanos := mtime % 1e9
		mtime /= 1e9 
		modtime := time.SecondsToUTC(mtime)
		m["nanos__"] = nanos // TODO
		timeStr := modtime.Format(time.RFC3339)
		m["unixMtime"] = timeStr
	}
	

	switch {
	case fi.IsRegular():
		if err = populateRegularFileMap(m, fileName, fi, sh); err != nil {
			return nil, err
		}
	case fi.IsSymlink():
	case fi.IsDirectory():
	case fi.IsBlock():
		fallthrough
	case fi.IsChar():
		fallthrough
	case fi.IsSocket():
		fallthrough
	case fi.IsFifo():
		return nil, UnimplementedError
	}
	return m, nil
}

func isValidUtf8(s string) bool {
	for _, rune := range []int(s) {
		if rune == 0xfffd {
			return false
		}
	}
	return true
}
