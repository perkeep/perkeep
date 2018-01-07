// Copyright 2013 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

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
	fi, err := f.Stat()
	if err != nil {
		panic(err)
	}
	tag, err := Decode(f, fi.Size())
	if err != nil {
		panic(err)
	}

	fmt.Println("Title:", tag.Title())
	fmt.Println("Artist:", tag.Artist())
	fmt.Println("Album:", tag.Album())
	fmt.Println("Genre:", tag.Genre())
	fmt.Println("Year:", tag.Year())
	fmt.Println("Disc:", tag.Disc())
	fmt.Println("Track:", tag.Track())
	fmt.Println("Performer:", tag.CustomFrames()["PERFORMER"])

	// Output:
	// Title: Test Name
	// Artist: Test Artist
	// Album: Test Album
	// Genre: Classical
	// Year: 2008-01-01 00:00:00 +0000 UTC
	// Disc: 3
	// Track: 7
	// Performer: Somebody
}
