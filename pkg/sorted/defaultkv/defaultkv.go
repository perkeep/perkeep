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

// Package defaultkv registers the sorted.KeyValue implementation named by
// sorted.DefaultKVFileType. Programs that open stores of that type should
// import this package for its side effect, rather than the implementation
// itself, so they follow any change to the default.
package defaultkv // import "perkeep.org/pkg/sorted/defaultkv"

import _ "perkeep.org/pkg/sorted/leveldb"
