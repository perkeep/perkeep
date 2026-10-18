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

// Package searchhelp provides offline help for the built-in search predicates
// without importing the search implementation. The generated help.json follows
// the server's registry; regenerate it with go generate ./internal/searchhelp
// after changing predicate names, descriptions, or registration order.
package searchhelp

import _ "embed"

//go:generate go run ./gen

//go:embed help.json
var helpJSON string

// SearchHelp returns JSON containing the built-in predicate names and descriptions.
// Unlike the server's live registry, it excludes optional handler-bound predicates.
func SearchHelp() string { return helpJSON }
