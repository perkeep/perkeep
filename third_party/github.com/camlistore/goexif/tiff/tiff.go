// Package tiff implements TIFF decoding as defined in TIFF 6.0 specification.
package tiff

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"io/ioutil"
)

// ReadAtReader is used when decoding Tiff tags and directories
type ReadAtReader interface {
	io.Reader
	io.ReaderAt
}

// Tiff provides access to decoded tiff data.
type Tiff struct {
	// Dirs is an ordered slice of the tiff's Image File Directories (IFDs).
	// The IFD at index 0 is IFD0.
	Dirs []*Dir
	// The tiff's byte-encoding (i.e. big/little endian).
	Order binary.ByteOrder
}

// Decode parses tiff-encoded data from r and returns a Tiff that reflects the
// structure and content of the tiff data. The first read from r should be the
// first byte of the tiff-encoded data (not necessarily the first byte of an
// os.File object).
func Decode(r io.Reader) (*Tiff, error) {
	data, err := ioutil.ReadAll(r)
	if err != nil {
		return nil, errors.New("tiff: could not read data")
	}
	buf := bytes.NewReader(data)

	t := new(Tiff)

	// read byte order
	bo := make([]byte, 2)
	n, err := buf.Read(bo)
	if n < len(bo) || err != nil {
		return nil, errors.New("tiff: could not read tiff byte order")
	} else {
		if string(bo) == "II" {
			t.Order = binary.LittleEndian
		} else if string(bo) == "MM" {
			t.Order = binary.BigEndian
		} else {
			return nil, errors.New("tiff: could not read tiff byte order")
		}
	}

	// check for special tiff marker
	var sp int16
	err = binary.Read(buf, t.Order, &sp)
	if err != nil || 0x002A != sp {
		return nil, errors.New("tiff: could not find special tiff marker")
	}

	// load offset to first IFD
	var offset int32
	err = binary.Read(buf, t.Order, &offset)
	if err != nil {
		return nil, errors.New("tiff: could not read offset to first IFD")
	}

	// load IFD's
	var d *Dir
	for offset != 0 {
		// seek to offset
		_, err := buf.Seek(int64(offset), 0)
		if err != nil {
			return nil, errors.New("tiff: seek to IFD failed")
		}

		if buf.Len() == 0 {
			return nil, errors.New("tiff: seek offset after EOF")
		}

		// load the dir
		d, offset, err = DecodeDir(buf, t.Order)
		if err != nil {
			return nil, err
		}
		t.Dirs = append(t.Dirs, d)
	}

	return t, nil
}

func (tf *Tiff) String() string {
	s := "Tiff{"
	for _, d := range tf.Dirs {
		s += d.String() + ", "
	}
	return s + "}"
}

// Dir reflects the parsed content of a tiff Image File Directory (IFD).
type Dir struct {
	Tags []*Tag
}

// DecodeDir parses a tiff-encoded IFD from r and returns a Dir object.  offset
// is the offset to the next IFD.  The first read from r should be at the first
// byte of the IFD. ReadAt offsets should be relative to the beginning of the
// tiff structure (not relative to the beginning of the IFD).
func DecodeDir(r ReadAtReader, order binary.ByteOrder) (d *Dir, offset int32, err error) {
	d = new(Dir)

	// get num of tags in ifd
	var nTags int16
	err = binary.Read(r, order, &nTags)
	if err != nil {
		return nil, 0, errors.New("tiff: falied to read IFD tag count: " + err.Error())
	}

	// load tags
	for n := 0; n < int(nTags); n++ {
		t, err := DecodeTag(r, order)
		if err != nil {
			return nil, 0, err
		}
		d.Tags = append(d.Tags, t)
	}

	// get offset to next ifd
	err = binary.Read(r, order, &offset)
	if err != nil {
		return nil, 0, errors.New("tiff: falied to read offset to next IFD: " + err.Error())
	}

	return d, offset, nil
}

func (d *Dir) String() string {
	s := "Dir{"
	for _, t := range d.Tags {
		s += t.String() + ", "
	}
	return s + "}"
}
