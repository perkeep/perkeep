//go:build deps
// +build deps

// Package deps depends on go modules in order to work around the fact that
// `go mod` subcommands will end up removing dependencies from the vendor directory
// if they are not referenced in any packages.
package deps

import (
	_ "github.com/goplusjs/gopherjs"       // UI
	_ "github.com/goplusjs/gopherjs/js"    // UI
	_ "honnef.co/go/js/dom"                // UI
	_ "honnef.co/go/tools/cmd/staticcheck" // Lint
	_ "myitcv.io/react/cmd/reactGen"       // UI
)
