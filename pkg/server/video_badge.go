/*
Copyright 2026 The Perkeep Authors.

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

package server

import (
	"image"
	"image/draw"
	"sync"

	"golang.org/x/image/vector"
)

// videoMaterialIcon returns the Material Design "videocam" icon of video
// thumbnails' badges.
var videoMaterialIcon = sync.OnceValues(func() (*fitnessMaterialIcon, error) {
	data, err := materialIconFiles.ReadFile("materialicons/videocam.svg")
	if err != nil {
		return nil, err
	}
	return parseFitnessMaterialSVG(data)
})

// drawVideoBadge returns a copy of a video's still, im, with a video badge
// at its bottom left, like fitness activity cards' badges, to tell videos
// from images in the web UI.
func drawVideoBadge(im image.Image) image.Image {
	icon, err := videoMaterialIcon()
	if err != nil {
		// An asset regression must fail the embedded-asset tests, not
		// break thumbnails.
		return im
	}
	b := im.Bounds()
	img := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(img, img.Bounds(), im, b.Min, draw.Src)
	pen := fitnessPen{vector.NewRasterizer(b.Dx(), b.Dy())}
	drawBadge(img, pen, func(cx, cy, size float64) {
		icon.draw(img, pen, cx, cy, size)
	})
	return img
}
