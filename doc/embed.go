// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package doc embeds the doc files into a Go binary.
package doc

import (
	"embed"
)

//go:embed *
var Root embed.FS
