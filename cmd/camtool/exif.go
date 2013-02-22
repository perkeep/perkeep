/*
Copyright 2013 Google Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"fmt"
	"log"
	"os"

	"camlistore.org/third_party/github.com/camlistore/goexif/exif"
)

func showEXIF(file string) {
	f, err := os.Open(file)
	if err != nil {
		panic(err.Error())
	}
	defer f.Close()
	ex, err := exif.Decode(f)
	if err != nil {
		log.Fatalf("exif.Decode: %v", err)
	}
	fmt.Printf("exif.Decode = %#v\n", ex)
	ct, err := ex.DateTime()
	fmt.Printf("exif.DateTime = %v, %v\n", ct, err)
}
