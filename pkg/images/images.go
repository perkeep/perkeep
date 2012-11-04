/*
Copyright 2012 Google Inc.

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

package images

import (
	"image"
	"io"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

type DecodeOpts struct {
	// Rotate specifies how to rotate the image.
	// If nil, the image is rotated automatically based on EXIF metadata.
	// If an int, Rotate is the number of degrees to rotate
	// counter clockwise and must be one of 0, 90, -90, 180, or
	// -180.
	Rotate interface{}

	// MaxWidgth and MaxHeight optionally specify bounds on the
	// final image's size.
	MaxWidth, MaxHeight int

	// TODO: consider alternate options if scaled ratio doesn't
	// match original ratio:
	//   Crop    bool   
	//   Stretch bool
}

// Decode decodes an image from r using the provided decoding options.
// If opts is nil, the defaults are used.
func Decode(r io.Reader, opts *DecodeOpts) (image.Image, error) {
	panic("TODO(mpl): implement")
}
