/*
Copyright 2017 The Camlistore Authors

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

package gphotos

import "testing"

func TestMediaURLsEqual(t *testing.T) {
	if !mediaURLsEqual("https://lh1.googleusercontent.com/foo.jpg", "https://lh100.googleusercontent.com/foo.jpg") {
		t.Fatal("want equal")
	}
	if mediaURLsEqual("https://foo.com/foo.jpg", "https://bar.com/foo.jpg") {
		t.Fatal("want not equal")
	}
}
