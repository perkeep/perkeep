/*
Copyright 2026 The Perkeep Authors

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

import "perkeep.org/pkg/blob"

// EXIFInfo is the indexed EXIF metadata for a file schema.
// It is retrieved on demand, separately from FileInfo and describe responses.
type EXIFInfo struct {
	FileRef  blob.Ref  `json:"fileRef"`
	WholeRef blob.Ref  `json:"wholeRef"`
	Tags     []EXIFTag `json:"tags"` // empty, not nil, when no tags are indexed
}

// EXIFTag is an indexed tag, identified by its directory and tag number.
type EXIFTag struct {
	Directory string   `json:"directory"` // IFD0, IFD1, Exif, or GPS
	Tag       string   `json:"tag"`       // lowercase, four-digit hexadecimal
	Name      string   `json:"name"`      // known field name, or "Unknown tag"
	Type      string   `json:"type"`      // string, int, rat, or float
	Values    []string `json:"values"`    // decoded strings or unmodified numeric components
}
