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

	main map[uint16]*tiff.Tag
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
		main: map[uint16]*tiff.Tag{},
		tif:  tif,
	}

	ifd0 := tif.Dirs[0]
	for _, tag := range ifd0.Tags {
		x.main[tag.Id] = tag
	}

	// recurse into exif, gps, and interop sub-IFDs
	if err = x.loadSubDir(er, exifPointer); err != nil {
		return x, err
	}
	if err = x.loadSubDir(er, gpsPointer); err != nil {
		return x, err
	}
	if err = x.loadSubDir(er, interopPointer); err != nil {
		return x, err
	}

	return x, nil
}

func (x *Exif) loadSubDir(r *bytes.Reader, tagId uint16) error {
	tag, ok := x.main[tagId]
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
		x.main[tag.Id] = tag
	}
	return nil
}

// Get retrieves the EXIF tag for the given field name.
//
// If the tag is not known or not present, an error is returned. If the
// tag name is known, the error will be a TagNotPresentError.
func (x *Exif) Get(name FieldName) (*tiff.Tag, error) {
	id, ok := fields[name]
	if !ok {
		return nil, fmt.Errorf("exif: invalid tag name %q", name)
	}
	if tg, ok := x.main[id]; ok {
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
	for name, _ := range fields {
		tag, err := x.Get(name)
		if isTagNotPresentErr(err) {
			continue
		} else if err != nil {
			panic("field list access/construction is broken - this should never happen")
		}

		err = w.Walk(name, tag)
		if err != nil {
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
	for name, id := range fields {
		if tag, ok := x.main[id]; ok {
			fmt.Fprintf(&buf, "%s: %s\n", name, tag)
		}
	}
	return buf.String()
}

func (x Exif) MarshalJSON() ([]byte, error) {
	m := map[string]interface{}{}

	for name, id := range fields {
		if tag, ok := x.main[id]; ok {
			m[string(name)] = tag
		}
	}

	return json.Marshal(m)
}

type appSec struct {
	marker byte
	data   []byte
}

// newAppSec finds marker in r and returns the corresponding application data
// section.
func newAppSec(marker byte, r io.Reader) (*appSec, error) {
	app := &appSec{marker: marker}

	buf := bufio.NewReader(r)

	// seek to marker
	for {
		b, err := buf.ReadByte()
		if err != nil {
			return nil, err
		}
		n, err := buf.Peek(1)
		if b == 0xFF && n[0] == marker {
			buf.ReadByte()
			break
		}
	}

	// read section size
	var dataLen uint16
	err := binary.Read(buf, binary.BigEndian, &dataLen)
	if err != nil {
		return nil, err
	}
	dataLen -= 2 // subtract length of the 2 byte size marker itself

	// read section data
	nread := 0
	for nread < int(dataLen) {
		s := make([]byte, int(dataLen)-nread)
		n, err := buf.Read(s)
		if err != nil {
			return nil, err
		}
		nread += n
		app.data = append(app.data, s...)
	}

	return app, nil
}

// reader returns a reader on this appSec.
func (app *appSec) reader() *bytes.Reader {
	return bytes.NewReader(app.data)
}

// exifReader returns a reader on this appSec with the read cursor advanced to
// the start of the exif's tiff encoded portion.
func (app *appSec) exifReader() (*bytes.Reader, error) {
	// read/check for exif special mark
	if len(app.data) < 6 {
		return nil, errors.New("exif: failed to find exif intro marker")
	}

	exif := app.data[:6]
	if string(exif) != "Exif"+string([]byte{0x00, 0x00}) {
		return nil, errors.New("exif: failed to find exif intro marker")
	}
	return bytes.NewReader(app.data[6:]), nil
}
