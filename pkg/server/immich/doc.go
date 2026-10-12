/*
Copyright 2026 The Perkeep Authors

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

// Package immich implements the upload-oriented subset of the Immich mobile
// API using native Perkeep files, permanodes and signed claims.
//
// Importing this package registers the "immich" HTTP handler constructor with
// blobserver. See README.md for configuration and compatibility limits.
package immich // import "perkeep.org/pkg/server/immich"
