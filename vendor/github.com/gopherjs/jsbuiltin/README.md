[![Build Status](https://api.travis-ci.org/gopherjs/jsbuiltin.svg?branch=master)](https://travis-ci.org/gopherjs/jsbuiltin) [![GoDoc](https://godoc.org/github.com/gopherjs/jsbuiltin?status.png)](http://godoc.org/github.com/gopherjs/jsbuiltin)

jsbuiltin - Built-in JavaScript functions for GopherJS
------------------------------------------------------

JavaScript has a small number of [built-in
functions](https://developer.mozilla.org/en/docs/Web/JavaScript/Reference/Global_Objects)
to handle some common day-to-day tasks. This package providers wrappers around
some of these functions for use in GopherJS.

It is worth noting that in many cases, using Go's equivalent functionality
(such as that found in the [net/url](https://golang.org/pkg/net/url/) package)
may be preferable to using this package, and will be a necessity any time you
wish to share functionality between front-end and back-end code.

### What is supported?
Not all JavaScript built-in functions make sense or are useful in a Go
environment. The table below shows each of the JavaScript built-in functions,
and its current state in this package.

| Name                 | Supported | Comment                     |
|----------------------|-----------|-----------------------------|
| eval()               | --        |                             |
| uneval()             | --        |                             |
| isFinite()           | yes       |                             |
| isNaN()              | yes       |                             |
| parseFloat()         | TODO?     | See note below              |
| parseInt()           | TODO?     | See note below              |
| decodeURI()          | yes       |                             |
| decodeURIComponent() | yes       |                             |
| encodeURI()          | yes       |                             |
| encodeURIComponent() | yes       |                             |
| escape()             | --        | deprecated circa 2000       |
| Number()             | --        | See note below              |
| String()             | --        | Use js.Object.String()      |
| unescape()           | --        | deprecated circa 2000       |
| typeof operator      | yes       |                             |
| instanceof operator  | yes       |                             |

#### Notes on unmplemented functions

* **eval()**: Is there ever a need to eval JS code from within Go?
* **Number()**: This requires handling a bunch of corner cases which don't
 normally exist in a strictly typed language such as Go. It seems that anyone
 with a legitimate need for this function probably needs to write their own
 wrapper to handle the cases that matter to them.
* **parseInt()** and **parseFloat()**: These could be added, but doing so
 will require answering some questions about the interfce. JavaScript has
 effectively two relevant data types (int and float) where Go has has 12.
 Deciding how to map JS's `parseInt()` to Go's `(u?)int(8|16|32|64)` types,
 and JS's `parseFloat()` Go's `float(32|64)` or `complex(64|128)` needs to
 be considered, as well as how to handle error cases (Go doesn't have a `NaN`
 type, so any `NaN` result probably needs to be converted to a proper Go
 error). If this matters to you, comments and/or PRs are welcome.

### Installation and Usage
Get or update this package and dependencies with:

```
go get -u -d -tags=js github.com/gopherjs/jsbuiltin
```

### Basic usage example

This is a modified version of the Pet example in the main GopherJS documentation,
to accept and return URI-encoded pet names using the jsbuiltin package.

```go
package main

import (
	"github.com/gopherjs/gopherjs/js"
	"github.com/gopherjs/jsbuiltin"
)

func main() {
	js.Global.Set("pet", map[string]interface{}{
		"New": New,
	})
}

type Pet struct {
	name string
}

func New(name string) *js.Object {
	return js.MakeWrapper(&Pet{name})
}

func (p *Pet) Name() string {
	return jsbuiltin.EncodeURIComponent(p.name)
}

func (p *Pet) SetName(uriComponent string) error {
	name, err := jsbuiltin.DecodeURIComponent(uriComponent)
	if err != nil {
		// Malformed UTF8 in uriComponent
		return err
	}
	p.name = name
	return nil
}
```
