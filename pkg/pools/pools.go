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

package pools

import (
	"bytes"
	"sync"
)

// bytesBuffer is a pool of *bytes.Buffer.
// Callers must Reset the buffer after obtaining it.
var bytesBuffer = sync.Pool{
	New: func() interface{} { return new(bytes.Buffer) },
}

// BytesBuffer returns an empty bytes.Buffer.
// It should be returned with PutBuffer.
func BytesBuffer() *bytes.Buffer {
	buf := bytesBuffer.Get().(*bytes.Buffer)
	buf.Reset()
	return buf
}

// PutBuffer returns a bytes.Buffer previously obtained with BytesBuffer.
func PutBuffer(buf *bytes.Buffer) {
	bytesBuffer.Put(buf)
}
