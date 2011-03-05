/*
Copyright 2011 Google Inc.

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
	"camli/blobref"
	"fmt"
//	"http"
	"os"
)

// Remove removes the blob.  An error is returned if the blob was failed to
// be removed.  Removal of a non-existent blob isn't an error.
func (c *Client) Remove(b *blobref.BlobRef) os.Error {
	url := fmt.Sprintf("%s/camli/post", c.server)
	return os.NewError(fmt.Sprintf("NOT IMPLEMENTED remove of %s to %s", b, url))
}
