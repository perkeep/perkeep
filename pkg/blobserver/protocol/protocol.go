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

// Package protocol contains types for Camlistore protocol types.
package protocol

import (
	"encoding/json"

	"camlistore.org/pkg/blob"
)

// StatResponse is the JSON document returned from the blob batch stat
// handler.
//
// See doc/protocol/blob-stat-protocol.txt.
type StatResponse struct {
	Stat        []blob.SizedRef `json:"stat"`
	CanLongPoll bool            `json:"canLongPoll"` // TODO: move this to discovery?
}

func (p *StatResponse) MarshalJSON() ([]byte, error) {
	v := *p
	if v.Stat == nil {
		v.Stat = []blob.SizedRef{}
	}
	return json.Marshal(v)
}

// UploadResponse is the JSON document returned from the blob batch
// upload handler.
//
// See doc/protocol/blob-upload-protocol.txt.
type UploadResponse struct {
	Received  []blob.SizedRef `json:"received"`
	ErrorText string          `json:"errorText,omitempty"`
}

func (p *UploadResponse) MarshalJSON() ([]byte, error) {
	v := *p
	if v.Received == nil {
		v.Received = []blob.SizedRef{}
	}
	return json.Marshal(v)
}
