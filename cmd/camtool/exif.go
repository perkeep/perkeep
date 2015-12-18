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

	"github.com/rwcarlsen/goexif/exif"
)

func showEXIF(file string) {
	f, err := os.Open(file)
	if err != nil {
		panic(err.Error())
	}
	defer f.Close()
	ex, err := exif.Decode(f)
	if err != nil {
		if exif.IsCriticalError(err) {
			log.Fatalf("exif.Decode, critical error: %v", err)
		}
		log.Printf("exif.Decode, warning: %v", err)
	}
	fmt.Printf("%v\n", ex)
	if exif.IsExifError(err) {
		// the error happened while decoding the EXIF sub-IFD, so as DateTime is
		// part of it, we have to assume (until there's a better "decode effort"
		// strategy in goexif) that it's not usable.
		return
	}
	ct, err := ex.DateTime()
	fmt.Printf("exif.DateTime = %v, %v\n", ct, err)
}
