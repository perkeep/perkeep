/*
Copyright 2014 The Camlistore Authors.

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

package client

import (
	"testing"

	"camlistore.org/pkg/types/clientconfig"
)

func TestAliasFromConfig(t *testing.T) {
	servers := map[string]*clientconfig.Server{
		"foo": {
			Server: "http://foo.com",
		},
		"foobar": {
			Server: "http://foo.com/bar",
		},
		"foobaz": {
			Server: "http://foo.com/baz",
		},
		"foobarlong": {
			Server: "http://foo.com/bar/long",
		},
	}
	config := &clientconfig.Config{
		Servers: servers,
	}
	urlWant := map[string]string{
		"http://foo.com/bs":              "foo",
		"http://foo.com":                 "foo",
		"http://foo.com/bar/index-mysql": "foobar",
		"http://foo.com/bar":             "foobar",
		"http://foo.com/baz/index-kv":    "foobaz",
		"http://foo.com/baz":             "foobaz",
		"http://foo.com/bar/long/disco":  "foobarlong",
		"http://foo.com/bar/long":        "foobarlong",
	}
	for url, want := range urlWant {
		alias := config.Alias(url)
		if alias == "" {
			t.Errorf("url %v matched nothing, wanted %v", url, want)
			continue
		}
		if alias != want {
			t.Errorf("url %v matched %v, wanted %v", url, alias, want)
		}
	}
}
