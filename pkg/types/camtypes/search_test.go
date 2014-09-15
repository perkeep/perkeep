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

import "testing"

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
