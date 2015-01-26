/*
Copyright 2011 The Camlistore Authors.

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

package mysql

import (
	"strconv"

	"camlistore.org/pkg/sorted"
)

const requiredSchemaVersion = 22

func SchemaVersion() int {
	return requiredSchemaVersion
}

// Note: using character set "binary", as any knowledge
// of character set encodings is handled by higher layers.
// At this layer we're just obeying the IndexStorage interface,
// which is purely about bytes.
func SQLCreateTables() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS /*DB*/.rows (
 k VARCHAR(` + strconv.Itoa(sorted.MaxKeySize) + `) NOT NULL PRIMARY KEY,
 v VARCHAR(` + strconv.Itoa(sorted.MaxValueSize) + `))
 DEFAULT CHARACTER SET binary`,

		`CREATE TABLE IF NOT EXISTS /*DB*/.meta (
 metakey VARCHAR(255) NOT NULL PRIMARY KEY,
 value VARCHAR(255) NOT NULL)
 DEFAULT CHARACTER SET binary`,
	}
}
