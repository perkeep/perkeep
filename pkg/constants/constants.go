/*
Copyright 2014 the Camlistore authors.

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

// Package constants contains Camlistore constants.
//
// This is a leaf package, without dependencies.
package constants

// MaxBlobSize is the size of a single blob in Camlistore.
//
// TODO: formalize this in the specs. This value of 16 MB is less than
// App Engine's 32 MB request limit, much more than Venti's limit, and
// much more than the ~64 KB & 256 KB chunks that the FileWriter make
const MaxBlobSize = 16 << 20
