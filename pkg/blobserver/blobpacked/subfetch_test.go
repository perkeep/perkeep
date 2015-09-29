/*
Copyright 2014 The Camlistore AUTHORS

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

package blobpacked

import (
	"fmt"
	"testing"
)

func TestCapOffsetLength(t *testing.T) {
	tests := []struct {
		size           uint32
		offset, length int64

		wantLen int64
		wantErr bool
	}{
		// Okay cases:
		{5, 0, 5, 5, false},
		{5, 1, 4, 4, false},

		// Length is capped:
		{5, 1, 9999, 4, false},

		// negative offset/length
		{5, -1, 5, 0, true},
		{5, 0, -1, 0, true},

		// offset too long:
		{5, 6, 1, 0, true},
	}
	for _, tt := range tests {
		gotLen, err := capOffsetLength(tt.size, tt.offset, tt.length)
		gotErr := err != nil
		if gotErr != tt.wantErr || gotLen != tt.wantLen {
			var want string
			if tt.wantErr {
				want = fmt.Sprintf("some error")
			} else {
				want = fmt.Sprintf("length %d, no error", tt.wantLen)
			}
			t.Errorf("capOffsetLength(%d, %d, %d) = (len %v, err %v); want %v", tt.size, tt.offset, tt.length, gotLen, err, want)
		}
	}
}
