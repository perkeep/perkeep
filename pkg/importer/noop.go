/*
Copyright 2014 The Perkeep Authors

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

package importer

import (
	"errors"
	"fmt"
	"net/http"
)

type todoImp struct {
	OAuth1 // for CallbackRequestAccount and CallbackURLParameters
	Props  Properties
}

func (t *todoImp) Properties() Properties {
	return t.Props
}

func (*todoImp) Run(*RunContext) error {
	return errors.New("fake error from todo importer")
}

func (*todoImp) IsAccountReady(acctNode *Object) (ok bool, err error) {
	return
}

func (*todoImp) SummarizeAccount(acctNode *Object) string { return "" }

func (*todoImp) ServeSetup(w http.ResponseWriter, r *http.Request, ctx *SetupContext) error {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, err := fmt.Fprintf(w, "The Setup page for the TODO importer.\nnode = %v\ncallback = %s\naccount URL = %s\n",
		ctx.AccountNode,
		ctx.CallbackURL(),
		"ctx.AccountURL()")
	return err
}

func (*todoImp) ServeCallback(w http.ResponseWriter, r *http.Request, ctx *SetupContext) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "The callback page for the TODO importer.\n") // nolint:errcheck
}
