package exif

import (
	"os"
	"testing"

	"camlistore.org/third_party/github.com/camlistore/goexif/tiff"
)

func TestDecode(t *testing.T) {
	name := "sample1.jpg"
	f, err := os.Open(name)
	if err != nil {
		t.Fatalf("%v\n", err)
	}

	x, err := Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if x == nil {
		t.Fatalf("No error and yet %v was not decoded\n", name)
	}

	val, err := x.Get("Model")
	t.Logf("Model: %v", val)
	t.Log(x)
}

type walker struct {
	t *testing.T
}

func (w *walker) Walk(name FieldName, tag *tiff.Tag) error {
	w.t.Logf("%v: %v", name, tag)
	return nil
}

func TestWalk(t *testing.T) {
	name := "sample1.jpg"
	f, err := os.Open(name)
	if err != nil {
		t.Fatalf("%v\n", err)
	}

	x, err := Decode(f)
	if err != nil {
		t.Error(err)
	}
	if x == nil {
		t.Fatal("bad err")
	}

	x.Walk(&walker{t})

}

func TestMarshal(t *testing.T) {
	name := "sample1.jpg"
	f, err := os.Open(name)
	if err != nil {
		t.Fatalf("%v\n", err)
	}
	defer f.Close()

	x, err := Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if x == nil {
		t.Fatal("bad err")
	}

	b, err := x.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("%s", b)
}
