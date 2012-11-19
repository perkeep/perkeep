package exif_test

import (
	"fmt"
	"log"
	"os"

	"camlistore.org/third_party/github.com/camlistore/goexif/exif"
)

func ExampleDecode() {
	fname := "sample1.jpg"

	f, err := os.Open(fname)
	if err != nil {
		log.Fatal(err)
	}

	x, err := exif.Decode(f)
	if err != nil {
		log.Fatal(err)
	}

	camModel, _ := x.Get("Model")
	date, _ := x.Get("DateTimeOriginal")
	fmt.Println(camModel.StringVal())
	fmt.Println(date.StringVal())

	focal, _ := x.Get("FocalLength")
	numer, denom := focal.Rat2(0) // retrieve first (only) rat. value
	fmt.Printf("%v/%v", numer, denom)
}
