/*
Copyright 2011 Google Inc.

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

package main

import (
	"http"
	"os"

	"camli/jsonconfig"
)

type JSONSignHandler struct {
}

func createJSONSignHandler(conf jsonconfig.Obj) (http.Handler, os.Error) {
	h := &JSONSignHandler{}
	//h.Stealth = conf.OptionalBool("stealth", false)
	if err := conf.Validate(); err != nil {
		return nil, err
	}
	return h, nil
}

func (h *JSONSignHandler) ServeHTTP(conn http.ResponseWriter, req *http.Request) {
	// TODO
}

