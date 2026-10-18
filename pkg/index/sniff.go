/*
Copyright 2011 The Perkeep Authors

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

package index

import (
	"perkeep.org/internal/blobsniffer"
	"perkeep.org/pkg/blob"
)

// BlobSniffer identifies a blob's MIME and Perkeep schema types.
// It is an alias for the implementation shared with command-line clients.
type BlobSniffer = blobsniffer.BlobSniffer

// NewBlobSniffer returns a sniffer for ref, which must be valid.
func NewBlobSniffer(ref blob.Ref) *BlobSniffer {
	return blobsniffer.New(ref)
}
