package taglib

import (
	"fmt"
	"os"
	_ "testing"
)

func ExampleDecode() {
	f, err := os.Open("testdata/test24.mp3")
	if err != nil {
		panic(err)
	}

	tag, err := Decode(f)
	if err != nil {
		panic(err)
	}

	fmt.Println("Title:", tag.Title())
	fmt.Println("Artist:", tag.Artist())
	fmt.Println("Album:", tag.Album())
	fmt.Println("Genre:", tag.Genre())
	fmt.Println("Year:", tag.Year())
	fmt.Println("Track:", tag.Track())

	// Output:
	// Title: Test Name
	// Artist: Test Artist
	// Album: Test Album
	// Genre: Classical
	// Year: 2008-01-01 00:00:00 +0000 UTC
	// Track: 7
}
