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

// Package secret resolves credentials supplied literally or through files.
// Values beginning with @ name files, with relative paths resolved from the
// current working directory. File contents have leading and trailing whitespace
// removed and may be empty. Values beginning with @@ escape a literal leading @.
// All other values are returned unchanged. Resolution is not recursive: neither
// file contents nor escaped values are interpreted again.
package secret // import "perkeep.org/internal/secret"

import (
	"fmt"
	"os"
	"strings"
)

// Resolve returns value literally, unless it begins with @. An @@ prefix
// escapes the leading @; otherwise the remainder names a file to read.
// Read errors wrap the underlying error without including file contents.
func Resolve(value string) (string, error) {
	if strings.HasPrefix(value, "@@") {
		return value[1:], nil
	}
	if !strings.HasPrefix(value, "@") {
		return value, nil
	}
	b, err := os.ReadFile(value[1:])
	if err != nil {
		return "", fmt.Errorf("reading secret file %q: %w", value[1:], err)
	}
	return strings.TrimSpace(string(b)), nil
}
