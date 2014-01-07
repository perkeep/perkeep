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

package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStatResponse(t *testing.T) {
	res := &StatResponse{
		CanLongPoll: true,
	}
	enc, err := json.MarshalIndent(res, "  ", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(enc); !strings.Contains(got, `"stat": []`) {
		t.Errorf("Wanted stat to have value []; got %s", got)
	}
}

func TestUploadResponse(t *testing.T) {
	res := &UploadResponse{}
	enc, err := json.MarshalIndent(res, "  ", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(enc); !strings.Contains(got, `"received": []`) {
		t.Errorf("Wanted received to have value []; got %s", got)
	}
}
