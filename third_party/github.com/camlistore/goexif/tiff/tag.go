package tiff

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"unicode"
	"unicode/utf8"
)

type FormatType int

const (
	IntVal FormatType = iota
	FloatVal
	RatVal
	StringVal
	UndefVal
	OtherVal
)

var fmtSize = map[uint16]uint32{
	1:  1,
	2:  1,
	3:  2,
	4:  4,
	5:  8,
	6:  1,
	7:  1,
	8:  2,
	9:  4,
	10: 8,
	11: 4,
	12: 8,
}

// Tag reflects the parsed content of a tiff IFD tag.
type Tag struct {
	// Id is the 2-byte tiff tag identifier
	Id uint16
	// Fmt is an integer (1 through 12) indicating the tag value's format.
	Fmt uint16
	// Ncomp is the number of type Fmt stored in the tag's value (i.e. the tag's
	// value is an array of type Fmt and length Ncomp).
	Ncomp uint32
	// Val holds the bytes that represent the tag's value.
	Val []byte

	order binary.ByteOrder

	intVals   []int64
	floatVals []float64
	ratVals   [][]int64
	strVal    string
}

// DecodeTag parses a tiff-encoded IFD tag from r and returns Tag object. The
// first read from r should be the first byte of the tag. ReadAt offsets should
// be relative to the beginning of the tiff structure (not relative to the
// beginning of the tag).
func DecodeTag(r ReadAtReader, order binary.ByteOrder) (*Tag, error) {
	t := new(Tag)
	t.order = order

	err := binary.Read(r, order, &t.Id)
	if err != nil {
		return nil, errors.New("tiff: tag id read failed: " + err.Error())
	}

	err = binary.Read(r, order, &t.Fmt)
	if err != nil {
		return nil, errors.New("tiff: tag format read failed: " + err.Error())
	}

	err = binary.Read(r, order, &t.Ncomp)
	if err != nil {
		return nil, errors.New("tiff: tag component count read failed: " + err.Error())
	}

	valLen := fmtSize[t.Fmt] * t.Ncomp
	var offset uint32
	if valLen > 4 {
		binary.Read(r, order, &offset)
		t.Val = make([]byte, valLen)
		n, err := r.ReadAt(t.Val, int64(offset))
		if n != int(valLen) || err != nil {
			return nil, errors.New("tiff: tag value read failed: " + err.Error())
		}
	} else {
		val := make([]byte, valLen)
		n, err := r.Read(val)
		if err != nil || n != int(valLen) {
			return nil, errors.New("tiff: tag offset read failed: " + err.Error())
		}

		n, err = r.Read(make([]byte, 4-valLen))
		if err != nil || n != 4-int(valLen) {
			return nil, errors.New("tiff: tag offset read failed: " + err.Error())
		}

		t.Val = val
	}

	t.convertVals()

	return t, nil
}

func (t *Tag) convertVals() {
	r := bytes.NewReader(t.Val)

	switch t.Fmt {
	case 2: // ascii string
		if len(t.Val) > 0 {
			t.strVal = string(t.Val[:len(t.Val)-1])
		}
	case 1:
		var v uint8
		t.intVals = make([]int64, int(t.Ncomp))
		for i := 0; i < int(t.Ncomp); i++ {
			err := binary.Read(r, t.order, &v)
			panicOn(err)
			t.intVals[i] = int64(v)
		}
	case 3:
		var v uint16
		t.intVals = make([]int64, int(t.Ncomp))
		for i := 0; i < int(t.Ncomp); i++ {
			err := binary.Read(r, t.order, &v)
			panicOn(err)
			t.intVals[i] = int64(v)
		}
	case 4:
		var v uint32
		t.intVals = make([]int64, int(t.Ncomp))
		for i := 0; i < int(t.Ncomp); i++ {
			err := binary.Read(r, t.order, &v)
			panicOn(err)
			t.intVals[i] = int64(v)
		}
	case 6:
		var v int8
		t.intVals = make([]int64, int(t.Ncomp))
		for i := 0; i < int(t.Ncomp); i++ {
			err := binary.Read(r, t.order, &v)
			panicOn(err)
			t.intVals[i] = int64(v)
		}
	case 8:
		var v int16
		t.intVals = make([]int64, int(t.Ncomp))
		for i := 0; i < int(t.Ncomp); i++ {
			err := binary.Read(r, t.order, &v)
			panicOn(err)
			t.intVals[i] = int64(v)
		}
	case 9:
		var v int32
		t.intVals = make([]int64, int(t.Ncomp))
		for i := 0; i < int(t.Ncomp); i++ {
			err := binary.Read(r, t.order, &v)
			panicOn(err)
			t.intVals[i] = int64(v)
		}
	case 5: // unsigned rational
		t.ratVals = make([][]int64, int(t.Ncomp))
		for i := 0; i < int(t.Ncomp); i++ {
			var n, d uint32
			err := binary.Read(r, t.order, &n)
			panicOn(err)
			err = binary.Read(r, t.order, &d)
			panicOn(err)
			t.ratVals[i] = []int64{int64(n), int64(d)}
		}
	case 10: // signed rational
		t.ratVals = make([][]int64, int(t.Ncomp))
		for i := 0; i < int(t.Ncomp); i++ {
			var n, d int32
			err := binary.Read(r, t.order, &n)
			panicOn(err)
			err = binary.Read(r, t.order, &d)
			panicOn(err)
			t.ratVals[i] = []int64{int64(n), int64(d)}
		}
	case 11: // float32
		t.floatVals = make([]float64, int(t.Ncomp))
		for i := 0; i < int(t.Ncomp); i++ {
			var v float32
			err := binary.Read(r, t.order, &v)
			panicOn(err)
			t.floatVals[i] = float64(v)
		}
	case 12: // float64 (double)
		t.floatVals = make([]float64, int(t.Ncomp))
		for i := 0; i < int(t.Ncomp); i++ {
			var u float64
			err := binary.Read(r, t.order, &u)
			panicOn(err)
			t.floatVals[i] = u
		}
	}
}

// Format returns a value indicating which method can be called to retrieve the
// tag's value properly typed (e.g. integer, rational, etc.).
func (t *Tag) Format() FormatType {
	switch t.Fmt {
	case 1, 3, 4, 6, 8, 9:
		return IntVal
	case 5, 10:
		return RatVal
	case 11, 12:
		return FloatVal
	case 2:
		return StringVal
	case 7:
		return UndefVal
	}
	return OtherVal
}

// Rat returns the tag's i'th value as a rational number. It panics if the tag format
// is not RatVal, if the denominator is zero, or if the tag has no i'th
// component. If a denominator could be zero, use Rat2.
func (t *Tag) Rat(i int) *big.Rat {
	n, d := t.Rat2(i)
	return big.NewRat(n, d)
}

// Rat2 returns the tag's i'th value as a rational number represented by a
// numerator-denominator pair. It panics if the tag format is not RatVal
// or if the tag value has no i'th component.
func (t *Tag) Rat2(i int) (num, den int64) {
	if t.Format() != RatVal {
		panic("Tag format is not 'rational'")
	}
	return t.ratVals[i][0], t.ratVals[i][1]
}

// Int returns the tag's i'th value as an integer. It panics if the tag format is not
// IntVal or if the tag value has no i'th component.
func (t *Tag) Int(i int) int64 {
	if t.Format() != IntVal {
		panic("Tag format is not 'int'")
	}
	return t.intVals[i]
}

// Float returns the tag's i'th value as a float. It panics if the tag format is not
// FloatVal or if the tag value has no i'th component.
func (t *Tag) Float(i int) float64 {
	if t.Format() != FloatVal {
		panic("Tag format is not 'float'")
	}
	return t.floatVals[i]
}

// StringVal returns the tag's value as a string. It panics if the tag
// format is not StringVal
func (t *Tag) StringVal() string {
	if t.Format() != StringVal {
		panic("Tag format is not 'ascii string'")
	}
	return t.strVal
}

// String returns a nicely formatted version of the tag.
func (t *Tag) String() string {
	data, err := t.MarshalJSON()
	panicOn(err)
	val := string(data)
	return fmt.Sprintf("{Id: %X, Val: %v}", t.Id, val)
}

func (t *Tag) MarshalJSON() ([]byte, error) {
	f := t.Format()

	switch f {
	case StringVal, UndefVal:
		return nullString(t.Val), nil
	case OtherVal:
		panic(fmt.Sprintf("Unhandled type Fmt=%v", t.Fmt))
	}

	rv := []string{}
	for i := 0; i < int(t.Ncomp); i++ {
		switch f {
		case RatVal:
			n, d := t.Rat2(i)
			rv = append(rv, fmt.Sprintf(`"%v/%v"`, n, d))
		case FloatVal:
			rv = append(rv, fmt.Sprintf("%v", t.Float(i)))
		case IntVal:
			rv = append(rv, fmt.Sprintf("%v", t.Int(i)))
		}
	}
	return []byte(fmt.Sprintf(`[%s]`, strings.Join(rv, ","))), nil
}

func nullString(in []byte) []byte {
	rv := bytes.Buffer{}
	rv.WriteByte('"')
	for _, b := range in {
		if unicode.IsPrint(rune(b)) {
			rv.WriteByte(b)
		}
	}
	rv.WriteByte('"')
	rvb := rv.Bytes()
	if utf8.Valid(rvb) {
		return rvb
	}
	return []byte(`""`)
}

func panicOn(err error) {
	if err != nil {
		panic("unexpected error: " + err.Error())
	}
}
