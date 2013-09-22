/*
Copyright 2013 The Camlistore Authors

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
	"os"
	"strings"
)

type Env struct {
	m     map[string]string
	order []string
}

func (e *Env) Set(k, v string) {
	_, dup := e.m[k]
	e.m[k] = v
	if !dup {
		e.order = append(e.order, k)
	}
}

func (e *Env) Del(k string) {
	delete(e.m, k)
}

// NoGo removes GOPATH and GOBIN.
func (e *Env) NoGo() {
	e.Del("GOPATH")
	e.Del("GOBIN")
}

func (e *Env) Flat() []string {
	vv := make([]string, 0, len(e.order))
	for _, k := range e.order {
		if v, ok := e.m[k]; ok {
			vv = append(vv, k+"="+v)
		}
	}
	return vv
}

func NewEnv() *Env {
	return &Env{make(map[string]string), nil}
}

func NewCopyEnv() *Env {
	env := NewEnv()
	for _, kv := range os.Environ() {
		eq := strings.Index(kv, "=")
		if eq > 0 {
			env.Set(kv[:eq], kv[eq+1:])
		}
	}
	return env
}
