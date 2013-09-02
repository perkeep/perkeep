package id3

// import (
// 	"os"
// 	"testing"
// )

// func TestParse(t *testing.T) {
// 	f, err := os.Open("/Users/hjfreyer/Downloads/01 Astronaut.mp3")
// 	if err != nil {
// 		t.Errorf("%v", err)
// 	}

// 	p, err := ParseTag(f)
// 	if err != nil {
// 		t.Errorf("%v", err)
// 	}
// 	t.Logf("%+v", p.(*Id3v23Tag))
// 	t.Logf("%+v", *p.(*Id3v23Tag).Frames["TALB"])

// 	s, err := p.Title()
// 	if err != nil {
// 		t.Errorf("%v", err)
// 	}

// 	t.Log(s)
// 	t.Fail()
// }
