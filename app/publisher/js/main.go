/*
Copyright 2016 The Camlistore Authors.

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

// This package contains source code for gopherjs, to generate javascript code
// that is included in the publisher web UI.
package main

import (
	"github.com/gopherjs/gopherjs/js"
	"github.com/gopherjs/jquery"
)

const (
	FIC = "div#fileitemcontainer"
)

var (
	jQuery = jquery.NewJQuery // convenience
)

func main() {
	//show jQuery Version on console:
	print("Your current jQuery version is: " + jQuery().Jquery)

	// export the RenderFile function, to make it visible to the importer
	// of publisher.js
	js.Global.Set("RenderFile", RenderFile)
}

// RenderFile is what will be called to render a file page in the subsequent CL.
// It does nothing for now, but it exists to show how the gopherjs generated code
// is inserted and used.
func RenderFile() {
	jQuery(FIC).Empty()
}
