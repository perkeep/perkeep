// Package exif implements decoding of EXIF data as defined in the EXIF 2.2
// specification (http://www.exif.org/Exif2-2.PDF).
package exif

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"strings"
	"time"

	"camlistore.org/third_party/github.com/camlistore/goexif/tiff"
)

const (
	jpeg_APP1 = 0xE1

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

// Parser allows the registration of custom parsing and field loading
// in the Decode function.
type Parser interface {
	// Parse should read data from x and insert parsed fields into x via
	// LoadTags.
	Parse(x *Exif) error
}

var parsers []Parser

func init() {
	RegisterParsers(&parser{})
}

// RegisterParsers registers one or more parsers to be automatically called
// when decoding EXIF data via the Decode function.
func RegisterParsers(ps ...Parser) {
	parsers = append(parsers, ps...)
}

type parser struct{}

func (p *parser) Parse(x *Exif) error {
	x.LoadTags(x.Tiff.Dirs[0], exifFields, false)

	// thumbnails
	if len(x.Tiff.Dirs) >= 2 {
		x.LoadTags(x.Tiff.Dirs[1], thumbnailFields, false)
	}

	// recurse into exif, gps, and interop sub-IFDs
	if err := loadSubDir(x, ExifIFDPointer, exifFields); err != nil {
		return err
	}
	if err := loadSubDir(x, GPSInfoIFDPointer, gpsFields); err != nil {
		return err
	}

	return loadSubDir(x, InteroperabilityIFDPointer, interopFields)
}

func loadSubDir(x *Exif, ptr FieldName, fieldMap map[uint16]FieldName) error {
	r := bytes.NewReader(x.Raw)

	tag, err := x.Get(ptr)
	if err != nil {
		return nil
	}
	offset := tag.Int(0)

	_, err = r.Seek(offset, 0)
	if err != nil {
		return errors.New("exif: seek to sub-IFD failed: " + err.Error())
	}
	subDir, _, err := tiff.DecodeDir(r, x.Tiff.Order)
	if err != nil {
		return errors.New("exif: sub-IFD decode failed: " + err.Error())
	}
	x.LoadTags(subDir, fieldMap, false)
	return nil
}

// Exif provides access to decoded EXIF metadata fields and values.
type Exif struct {
	Tiff *tiff.Tiff
	main map[FieldName]*tiff.Tag
	Raw  []byte
}

// Decode parses EXIF-encoded data from r and returns a queryable Exif
// object. After the exif data section is called and the tiff structure
// decoded, each registered parser is called (in order of registration). If
// one parser returns an error, decoding terminates and the remaining
// parsers are not called.
func Decode(r io.Reader) (*Exif, error) {
	// EXIF data in JPEG is stored in the APP1 marker. EXIF data uses the TIFF
	// format to store data.
	// If we're parsing a TIFF image, we don't need to strip away any data.
	// If we're parsing a JPEG image, we need to strip away the JPEG APP1
	// marker and also the EXIF header.

	header := make([]byte, 4)
	n, err := r.Read(header)
	if err != nil {
		return nil, err
	}
	if n < len(header) {
		return nil, errors.New("exif: short read on header")
	}

	var isTiff bool
	switch string(header) {
	case "II*\x00":
		// TIFF - Little endian (Intel)
		isTiff = true
	case "MM\x00*":
		// TIFF - Big endian (Motorola)
		isTiff = true
	default:
		// Not TIFF, assume JPEG
	}

	// Put the header bytes back into the reader.
	r = io.MultiReader(bytes.NewReader(header), r)
	var (
		er  *bytes.Reader
		tif *tiff.Tiff
	)

	if isTiff {
		// Functions below need the IFDs from the TIFF data to be stored in a
		// *bytes.Reader.  We use TeeReader to get a copy of the bytes as a
		// side-effect of tiff.Decode() doing its work.
		b := &bytes.Buffer{}
		tr := io.TeeReader(r, b)
		tif, err = tiff.Decode(tr)
		er = bytes.NewReader(b.Bytes())
	} else {
		// Locate the JPEG APP1 header.
		var sec *appSec
		sec, err = newAppSec(jpeg_APP1, r)
		if err != nil {
			return nil, err
		}
		// Strip away EXIF header.
		er, err = sec.exifReader()
		if err != nil {
			return nil, err
		}
		tif, err = tiff.Decode(er)
	}

	if err != nil {
		return nil, fmt.Errorf("exif: decode failed (%v) ", err)
	}

	er.Seek(0, 0)
	raw, err := ioutil.ReadAll(er)
	if err != nil {
		return nil, fmt.Errorf("exif: decode failed (%v) ", err)
	}

	// build an exif structure from the tiff
	x := &Exif{
		main: map[FieldName]*tiff.Tag{},
		Tiff: tif,
		Raw:  raw,
	}

	for i, p := range parsers {
		if err := p.Parse(x); err != nil {
			return x, fmt.Errorf("exif: parser %v failed (%v)", i, err)
		}
	}

	return x, nil
}

// LoadTags loads tags into the available fields from the tiff Directory
// using the given tagid-fieldname mapping.  Used to load makernote and
// other meta-data.  If showMissing is true, tags in d that are not in the
// fieldMap will be loaded with the FieldName UnknownPrefix followed by the
// tag ID (in hex format).
func (x *Exif) LoadTags(d *tiff.Dir, fieldMap map[uint16]FieldName, showMissing bool) {
	for _, tag := range d.Tags {
		name := fieldMap[tag.Id]
		if name == "" {
			if !showMissing {
				continue
			}
			name = FieldName(fmt.Sprintf("%v%x", UnknownPrefix, tag.Id))
		}
		x.main[name] = tag
	}
}

// Get retrieves the EXIF tag for the given field name.
//
// If the tag is not known or not present, an error is returned. If the
// tag name is known, the error will be a TagNotPresentError.
func (x *Exif) Get(name FieldName) (*tiff.Tag, error) {
	if tg, ok := x.main[name]; ok {
		return tg, nil
	}
	return nil, TagNotPresentError(name)
}

// Walker is the interface used to traverse all fields of an Exif object.
type Walker interface {
	// Walk is called for each non-nil EXIF field. Returning a non-nil
	// error aborts the walk/traversal.
	Walk(name FieldName, tag *tiff.Tag) error
}

// Walk calls the Walk method of w with the name and tag for every non-nil
// EXIF field.  If w aborts the walk with an error, that error is returned.
func (x *Exif) Walk(w Walker) error {
	for name, tag := range x.main {
		if err := w.Walk(name, tag); err != nil {
			return err
		}
	}
	return nil
}

// DateTime returns the EXIF's "DateTimeOriginal" field, which
// is the creation time of the photo. If not found, it tries
// the "DateTime" (which is meant as the modtime) instead.
// The error will be TagNotPresentErr if none of those tags
// were found, or a generic error if the tag value was
// not a string, or the error returned by time.Parse.
//
// If the EXIF lacks timezone information or GPS time, the returned
// time's Location will be time.Local.
func (x *Exif) DateTime() (time.Time, error) {
	var dt time.Time
	tag, err := x.Get(DateTimeOriginal)
	if err != nil {
		tag, err = x.Get(DateTime)
		if err != nil {
			return dt, err
		}
	}
	if tag.TypeCategory() != tiff.StringVal {
		return dt, errors.New("DateTime[Original] not in string format")
	}
	exifTimeLayout := "2006:01:02 15:04:05"
	dateStr := strings.TrimRight(string(tag.Val), "\x00")
	// TODO(bradfitz,mpl): look for timezone offset, GPS time, etc.
	// For now, just always return the time.Local timezone.
	return time.ParseInLocation(exifTimeLayout, dateStr, time.Local)
}

func ratFloat(num, dem int64) float64 {
	return float64(num) / float64(dem)
}

func tagDegrees(tag *tiff.Tag) float64 {
	return ratFloat(tag.Rat2(0)) + ratFloat(tag.Rat2(1))/60 + ratFloat(tag.Rat2(2))/3600
}

// LatLong returns the latitude and longitude of the photo and
// whether it was present.
func (x *Exif) LatLong() (lat, long float64, ok bool) {
	longTag, err := x.Get(FieldName("GPSLongitude"))
	if err != nil {
		return
	}
	ewTag, err := x.Get(FieldName("GPSLongitudeRef"))
	if err != nil {
		return
	}
	latTag, err := x.Get(FieldName("GPSLatitude"))
	if err != nil {
		return
	}
	nsTag, err := x.Get(FieldName("GPSLatitudeRef"))
	if err != nil {
		return
	}
	long = tagDegrees(longTag)
	lat = tagDegrees(latTag)
	if ewTag.StringVal() == "W" {
		long *= -1.0
	}
	if nsTag.StringVal() == "S" {
		lat *= -1.0
	}
	return lat, long, true
}

// String returns a pretty text representation of the decoded exif data.
func (x *Exif) String() string {
	var buf bytes.Buffer
	for name, tag := range x.main {
		fmt.Fprintf(&buf, "%s: %s\n", name, tag)
	}
	return buf.String()
}

// JpegThumbnail returns the jpeg thumbnail if it exists. If it doesn't exist,
// TagNotPresentError will be returned
func (x *Exif) JpegThumbnail() ([]byte, error) {
	offset, err := x.Get(ThumbJPEGInterchangeFormat)
	if err != nil {
		return nil, err
	}
	length, err := x.Get(ThumbJPEGInterchangeFormatLength)
	if err != nil {
		return nil, err
	}
	return x.Raw[offset.Int(0) : offset.Int(0)+length.Int(0)], nil
}

// MarshalJson implements the encoding/json.Marshaler interface providing output of
// all EXIF fields present (names and values).
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
	var dataLen int

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
		dataLen = int(binary.BigEndian.Uint16(dataLenBytes))
	}

	// read section data
	nread := 0
	for nread < dataLen {
		s := make([]byte, dataLen-nread)
		n, err := br.Read(s)
		nread += n
		if err != nil && nread < dataLen {
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
