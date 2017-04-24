/*
Copyright 2014 The Perkeep Authors

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

type StatusError struct {
	Error string `json:"error"`
	URL   string `json:"url,omitempty"` // optional
}

// ShareImportProgress is the state of a share importing process.
type ShareImportProgress struct {
	// Assembled is whether the share being imported is for an assembled file.
	Assembled bool
	// Running is whether the import share handler is currently running an import.
	Running bool
	// FilesSeen is the number of files, children of the top directory being shared, that have been discovered so far.
	FilesSeen int
	// FilesCopied is the number of files that have already been imported during that import.
	FilesCopied int
	// BlobRef is the ref of the schema of the file or top directory being imported.
	BlobRef blob.Ref
}
