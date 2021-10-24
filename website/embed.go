// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package website embeds the perkeep.org website resources.
package website

import (
	"embed"
)

//go:embed content scripts static talks testdata tmpl
var Root embed.FS
