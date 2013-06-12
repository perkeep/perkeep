/*
Copyright 2012 Google Inc.

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

package sqlite

import (
	"log"
	"os/exec"
	"strings"
)

const requiredSchemaVersion = 1

func SchemaVersion() int {
	return requiredSchemaVersion
}

func SQLCreateTables() []string {
	return []string{
		`CREATE TABLE rows (
 k VARCHAR(255) NOT NULL PRIMARY KEY,
 v VARCHAR(255))`,

		`CREATE TABLE meta (
 metakey VARCHAR(255) NOT NULL PRIMARY KEY,
 value VARCHAR(255) NOT NULL)`,
	}
}

// IsWALCapable checks if the installed sqlite3 library can
// use Write-Ahead Logging (i.e version >= 3.7.0)
func IsWALCapable() bool {
	// TODO(mpl): alternative to make it work on windows
	cmdPath, err := exec.LookPath("pkg-config")
	if err != nil {
		log.Printf("Could not find pkg-config to check sqlite3 lib version: %v", err)
		return false
	}
	out, err := exec.Command(cmdPath, "--modversion", "sqlite3").Output()
	if err != nil {
		log.Printf("Could not check sqlite3 version: %v\n", err)
		return false
	}
	version := strings.TrimRight(string(out), "\n")
	return version >= "3.7.0"
}

// EnableWAL returns the statement to enable Write-Ahead Logging,
// which improves SQLite concurrency.
// Requires SQLite >= 3.7.0
func EnableWAL() string {
	return "PRAGMA journal_mode = WAL"
}
