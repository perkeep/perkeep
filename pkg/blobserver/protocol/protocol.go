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

// Package protocol contains types for Perkeep protocol types.
package protocol // import "perkeep.org/pkg/blobserver/protocol"

import (
	"encoding/json"

	"perkeep.org/pkg/blob"
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
	Received []blob.SizedRef `json:"received"`
	// Accepted are the blobs added to the upload session the upload was
	// in, which aren't yet known to be durable.
	Accepted  []blob.SizedRef `json:"accepted,omitempty"`
	ErrorText string          `json:"errorText,omitempty"`
}

func (p *UploadResponse) MarshalJSON() ([]byte, error) {
	v := *p
	if v.Received == nil {
		v.Received = []blob.SizedRef{}
	}
	return json.Marshal(v)
}

// SessionOpenResponse is the JSON document returned when an upload
// session is opened.
//
// See doc/protocol/blob-upload-session.md.
type SessionOpenResponse struct {
	// Session is the session's ID.
	Session string `json:"session"`
	// IdleTimeoutSeconds is how long the session can be idle before the
	// server closes it.
	IdleTimeoutSeconds int `json:"idleTimeoutSeconds"`
	// MaxBufferedBytes is how many bytes of blobs the session can buffer.
	MaxBufferedBytes int64 `json:"maxBufferedBytes"`
}

// SessionStatusResponse is the JSON document returned from flushing an
// upload session, or asking its status.
//
// See doc/protocol/blob-upload-session.md.
type SessionStatusResponse struct {
	// Durable are the blobs added to the session that are durable, and
	// that the client hasn't acknowledged.
	Durable []blob.SizedRef `json:"durable"`
	// Failed are the blobs that failed to commit since the session's last
	// flush or status. They're no longer in the session.
	Failed []FailedBlob `json:"failed,omitempty"`
}

func (p *SessionStatusResponse) MarshalJSON() ([]byte, error) {
	v := *p
	if v.Durable == nil {
		v.Durable = []blob.SizedRef{}
	}
	return json.Marshal(v)
}

// FailedBlob is a blob that an operation failed for, and why.
type FailedBlob struct {
	BlobRef blob.Ref `json:"blobRef"`
	Error   string   `json:"error"`
}
