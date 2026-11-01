/*
Copyright 2026 The Perkeep Authors.

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

import "time"

// SecretMetadata is the non-secret metadata for a server-side credential.
type SecretMetadata struct {
	// Name is a logical slash-separated identifier, not a filesystem path.
	// Names are 1 to 512 bytes. Segments contain only ASCII letters, digits,
	// '.', '_', ':', or '-'; empty, "." and ".." segments are forbidden.
	// Names are metadata and must not contain secret material.
	Name string `json:"name"`

	// Revision is the name's monotonically increasing stored revision.
	// It is positive for stored records, including deletion tombstones, and
	// zero for in-process snapshots of a never-present name.
	Revision int64 `json:"revision"`

	// MigratedFromPermanode reports that a value under this name was stored
	// in immutable permanode claims and may remain in history and backups.
	// Ordinary writes preserve this warning. Clear it explicitly only after
	// rotating the credential or otherwise addressing that exposure.
	MigratedFromPermanode bool `json:"migratedFromPermanode"`

	// Updated is the server-assigned UTC time of the last committed mutation.
	// It is always nonzero in successful HTTP API replies. Only in-process
	// snapshots of a never-present name use the zero time.
	Updated time.Time `json:"updated"`
}

// SecretEntry is a server-side credential's metadata and plaintext value.
// The HTTP API returns it only for an explicit value read.
type SecretEntry struct {
	// SecretMetadata identifies the credential and describes its stored state.
	SecretMetadata

	// Value is the credential's plaintext, at most 1 MiB of valid UTF-8.
	// Empty values are allowed, and whitespace is preserved exactly.
	Value string `json:"value"`
}
