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

package client

import (
	"fmt"
)

type Stats struct {
	// The number of uploads that were requested, but perhaps
	// not actually performed if the server already had the items.
	UploadRequests ByCountAndBytes

	// The uploads which were actually sent to the blobserver
	// due to the server not having the blobs
	Uploads ByCountAndBytes
}

func (s *Stats) String() string {
	return "[uploadRequests=" + s.UploadRequests.String() + " uploads=" + s.Uploads.String() + "]"
}

type ByCountAndBytes struct {
	Blobs int
	Bytes int64
}

func (bb *ByCountAndBytes) String() string {
	return fmt.Sprintf("[blobs=%d bytes=%d]", bb.Blobs, bb.Bytes)
}
