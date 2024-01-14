/*
Copyright 2017 The Perkeep Authors

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

/*
TODO(steve.armstrong): Until app/scanningcabinet/handler.go handleUiFile()
properly manages Content-Type, be sure to update it whenever adding a new
file type to the pattern below.
*/

// Package ui contains the embedded JS/CSS assets for the scanningcabinet app.
package ui // import "perkeep.org/app/scanningcabinet/ui"

import "embed"

//go:embed *.js *.css
var Files embed.FS
