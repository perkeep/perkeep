// Package exif implements decoding of EXIF data as defined in the EXIF 2.2
// specification.
package exif

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"camlistore.org/third_party/github.com/camlistore/goexif/tiff"
)

var validField map[FieldName]bool

func init() {
	validField = make(map[FieldName]bool)
	for _, name := range exifFields {
		validField[name] = true
	}
	for _, name := range gpsFields {
		validField[name] = true
	}
	for _, name := range interopFields {
		validField[name] = true
	}
}

const (
	exifPointer    = 0x8769
	gpsPointer     = 0x8825
	interopPointer = 0xA005
)

// A TagNotPresentError is returned when the requested field is not
// present in the EXIF.
type TagNotPresentError FieldName

func (tag TagNotPresentError) Error() string {
	return fmt.Sprintf("exif: tag %q is not present", string(tag))
}

func isTagNotPresentErr(err error) bool {
	_, ok := err.(TagNotPresentError)
	return ok
}

type Exif struct {
	tif *tiff.Tiff

	main map[FieldName]*tiff.Tag
}

// Decode parses EXIF-encoded data from r and returns a queryable Exif object.
func Decode(r io.Reader) (*Exif, error) {
	sec, err := newAppSec(0xE1, r)
	if err != nil {
		return nil, err
	}
	er, err := sec.exifReader()
	if err != nil {
		return nil, err
	}
	tif, err := tiff.Decode(er)
	if err != nil {
		return nil, errors.New("exif: decode failed: " + err.Error())
	}

	// build an exif structure from the tiff
	x := &Exif{
		main: map[FieldName]*tiff.Tag{},
		tif:  tif,
	}

	ifd0 := tif.Dirs[0]
	for _, tag := range ifd0.Tags {
		name := exifFields[tag.Id]
		x.main[name] = tag
	}

	// recurse into exif, gps, and interop sub-IFDs
	if err = x.loadSubDir(er, exifIFDPointer, exifFields); err != nil {
		return x, err
	}
	if err = x.loadSubDir(er, gpsInfoIFDPointer, gpsFields); err != nil {
		return x, err
	}
	if err = x.loadSubDir(er, interoperabilityIFDPointer, interopFields); err != nil {
		return x, err
	}

	return x, nil
}

func (x *Exif) loadSubDir(r *bytes.Reader, ptrName FieldName, fieldMap map[uint16]FieldName) error {
	tag, ok := x.main[ptrName]
	if !ok {
		return nil
	}
	offset := tag.Int(0)

	_, err := r.Seek(offset, 0)
	if err != nil {
		return errors.New("exif: seek to sub-IFD failed: " + err.Error())
	}
	subDir, _, err := tiff.DecodeDir(r, x.tif.Order)
	if err != nil {
		return errors.New("exif: sub-IFD decode failed: " + err.Error())
	}
	for _, tag := range subDir.Tags {
		name := fieldMap[tag.Id]
		x.main[name] = tag
	}
	return nil
}

// Get retrieves the EXIF tag for the given field name.
//
// If the tag is not known or not present, an error is returned. If the
// tag name is known, the error will be a TagNotPresentError.
func (x *Exif) Get(name FieldName) (*tiff.Tag, error) {
	if !validField[name] {
		return nil, fmt.Errorf("exif: invalid tag name %q", name)
	} else if tg, ok := x.main[name]; ok {
		return tg, nil
	}
	return nil, TagNotPresentError(name)
}

// Walker is the interface used to traverse all exif fields of an Exif object.
// Returning a non-nil error aborts the walk/traversal.
type Walker interface {
	Walk(name FieldName, tag *tiff.Tag) error
}

// Walk calls the Walk method of w with the name and tag for every non-nil exif
// field.
func (x *Exif) Walk(w Walker) error {
	for name, tag := range x.main {
		if err := w.Walk(name, tag); err != nil {
			return err
		}
	}
	return nil
}

// DateTime returns the EXIF's "DateTime" field, which is
// the creation time of the photo.
// The error will be TagNotPresentErr if the DateTime tag
// was not found, or a generic error if the tag value was
// not a string, or the error returned by time.Parse.
func (x *Exif) DateTime() (time.Time, error) {
	// TODO(mpl): investigate the time zone question. exif -l
	// shows a TimeZoneOffset field, but it's empty for the test
	// pic I've used, and wikipedia says there's no time zone
	// info in EXIF...
	var dt time.Time
	tag, err := x.Get(DateTime)
	if err != nil {
		return dt, err
	}
	if tag.Format() != tiff.StringVal {
		return dt, errors.New("DateTime not in string format")
	}
	exifTimeLayout := "2006:01:02 15:04:05"
	dateStr := strings.TrimRight(string(tag.Val), "\x00")
	return time.Parse(exifTimeLayout, dateStr)
}

// String returns a pretty text representation of the decoded exif data.
func (x *Exif) String() string {
	var buf bytes.Buffer
	for name, tag := range x.main {
		fmt.Fprintf(&buf, "%s: %s\n", name, tag)
	}
	return buf.String()
}

func (x Exif) MarshalJSON() ([]byte, error) {
	return json.Marshal(x.main)
}

type appSec struct {
	marker byte
	data   []byte
}

// newAppSec finds marker in r and returns the corresponding application data
// section.
func newAppSec(marker byte, r io.Reader) (*appSec, error) {
	br := bufio.NewReader(r)
	app := &appSec{marker: marker}
	var dataLen uint16

	// seek to marker
	for dataLen == 0 {
		if _, err := br.ReadBytes(0xFF); err != nil {
			return nil, err
		}
		c, err := br.ReadByte()
		if err != nil {
			return nil, err
		} else if c != marker {
			continue
		}

		dataLenBytes, err := br.Peek(2)
		if err != nil {
			return nil, err
		}
		dataLen = binary.BigEndian.Uint16(dataLenBytes)
	}

	// read section data
	nread := 0
	for nread < int(dataLen) {
		s := make([]byte, int(dataLen)-nread)
		n, err := br.Read(s)
		nread += n
		if err != nil && nread < int(dataLen) {
			return nil, err
		}
		app.data = append(app.data, s[:n]...)
	}
	app.data = app.data[2:] // exclude dataLenBytes
	return app, nil
}

// reader returns a reader on this appSec.
func (app *appSec) reader() *bytes.Reader {
	return bytes.NewReader(app.data)
}

// exifReader returns a reader on this appSec with the read cursor advanced to
// the start of the exif's tiff encoded portion.
func (app *appSec) exifReader() (*bytes.Reader, error) {
	if len(app.data) < 6 {
		return nil, errors.New("exif: failed to find exif intro marker")
	}

	// read/check for exif special mark
	exif := app.data[:6]
	if !bytes.Equal(exif, append([]byte("Exif"), 0x00, 0x00)) {
		return nil, errors.New("exif: failed to find exif intro marker")
	}
	return bytes.NewReader(app.data[6:]), nil
}
