/*
Copyright 2014 The Camlistore Authors

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

package camtypes

import (
	"reflect"
	"testing"
)

var fileInfoVideoTable = []struct {
	fi    *FileInfo
	video bool
}{
	{&FileInfo{FileName: "some.mp4", MIMEType: "application/octet-stream"}, true},
	{&FileInfo{FileName: "IMG_1231.MOV", MIMEType: "application/octet-stream"}, true},
	{&FileInfo{FileName: "movie.mkv", MIMEType: "application/octet-stream"}, true},
	{&FileInfo{FileName: "movie.məv", MIMEType: "application/octet-stream"}, false},
	{&FileInfo{FileName: "tape", MIMEType: "video/webm"}, true},
	{&FileInfo{FileName: "tape", MIMEType: "application/ogg"}, false},
	{&FileInfo{FileName: "IMG_12312.jpg", MIMEType: "application/octet-stream"}, false},
	{&FileInfo{FileName: "IMG_12312.jpg", MIMEType: "image/jpeg"}, false},
}

func TestIsVideo(t *testing.T) {
	for _, example := range fileInfoVideoTable {
		if example.fi.IsVideo() != example.video {
			t.Errorf("IsVideo failed video=%t filename=%s mimetype=%s",
				example.video, example.fi.FileName, example.fi.MIMEType)
		}
	}
}

func TestExpandLocationArea(t *testing.T) {
	tt := []struct {
		comment string
		before  *LocationBounds
		input   Location
		want    *LocationBounds
	}{
		// Until further notice, these tests are a series, i.e. want becomes before in
		// each subsequent test.
		{
			comment: "uninitialized, add point",
			before:  nil,
			input: Location{
				Latitude:  35.,
				Longitude: -90.,
			},
			want: &LocationBounds{
				North: 35.,
				West:  -90.,
				South: 35.,
				East:  -90.,
			},
		},
		{
			comment: "one point, expand north and west",
			before: &LocationBounds{
				North: 35.,
				West:  -90.,
				South: 35.,
				East:  -90.,
			},
			input: Location{
				Latitude:  40.,
				Longitude: -100.,
			},
			want: &LocationBounds{
				North: 40.,
				West:  -100.,
				South: 35.,
				East:  -90.,
			},
		},
		{
			comment: "area not yet crossing antimeridian, expand west over it",
			before: &LocationBounds{
				North: 40.,
				West:  -100.,
				South: 35.,
				East:  -90.,
			},
			input: Location{
				Latitude:  37.,
				Longitude: 170.,
			},
			want: &LocationBounds{
				North: 40.,
				West:  170.,
				South: 35.,
				East:  -90.,
			},
		},
		{
			comment: "area spanning over antimeridian, expand east",
			before: &LocationBounds{
				North: 40.,
				West:  170.,
				South: 35.,
				East:  -90.,
			},
			input: Location{
				Latitude:  -20.,
				Longitude: 20.,
			},
			want: &LocationBounds{
				North: 40.,
				West:  170.,
				South: -20.,
				East:  20.,
			},
		},

		// New series here.
		{
			comment: "area not yet crossing antimeridian, expand east over it",
			before: &LocationBounds{
				North: 40.,
				West:  120.,
				South: 35.,
				East:  160.,
			},
			input: Location{
				Latitude:  -20,
				Longitude: -160,
			},
			want: &LocationBounds{
				North: 40.,
				West:  120.,
				South: -20.,
				East:  -160.,
			},
		},
		{
			comment: "area spanning over antimeridian, expand west",
			before: &LocationBounds{
				North: 40.,
				West:  120.,
				South: -20.,
				East:  -160.,
			},
			input: Location{
				Latitude:  0.,
				Longitude: 100.,
			},
			want: &LocationBounds{
				North: 40.,
				West:  100.,
				South: -20.,
				East:  -160.,
			},
		},
	}

	for _, v := range tt {
		lb := v.before.Expand(v.input)
		// TODO(mpl): come back and understand why deepEqual is needed. probably some float shenanigans.
		if !reflect.DeepEqual(v.want, lb) {
			t.Fatalf("for %q expansion: wanted %#v, got %#v", v.comment, v.want, lb)
		}
	}
}

func TestWrap180(t *testing.T) {
	tt := []struct {
		input float64
		want  float64
	}{
		{
			input: -50,
			want:  -50,
		},
		{
			input: 50,
			want:  50,
		},
		{
			input: -190.2,
			want:  169.8,
		},
		{
			input: 190.3,
			want:  -169.7,
		},
		{
			input: -362,
			want:  -2,
		},
		{
			input: 362,
			want:  2,
		},
	}
	for k, v := range tt {
		got := Longitude(v.input).WrapTo180()
		if got != v.want {
			t.Errorf("test %d: wanted %f, got %f", k, v.want, got)
		}
	}
}
