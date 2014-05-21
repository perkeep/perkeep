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

package auth

import (
	"fmt"
	"net/http"
	"reflect"
	"testing"
)

func TestFromConfig(t *testing.T) {
	tests := []struct {
		in string

		want    interface{}
		wanterr interface{}
	}{
		{in: "", wanterr: ErrNoAuth},
		{in: "slkdjflksdjf", wanterr: `Unknown auth type: "slkdjflksdjf"`},
		{in: "none", want: None{}},
		{in: "localhost", want: Localhost{}},
		{in: "userpass:alice:secret", want: &UserPass{Username: "alice", Password: "secret", OrLocalhost: false, VivifyPass: ""}},
		{in: "userpass:alice:secret:+localhost", want: &UserPass{Username: "alice", Password: "secret", OrLocalhost: true, VivifyPass: ""}},
		{in: "userpass:alice:secret:+localhost:vivify=foo", want: &UserPass{Username: "alice", Password: "secret", OrLocalhost: true, VivifyPass: "foo"}},
		{in: "devauth:port3179", want: &DevAuth{Password: "port3179", VivifyPass: "viviport3179"}},
		{in: "basic:alice:secret", want: &UserPass{Username: "alice", Password: "secret", OrLocalhost: false, VivifyPass: ""}},
		{in: "basic:alice:secret:+localhost", wanterr: `invalid basic auth syntax. got "alice:secret:+localhost", want "username:password"`},
		{in: "basic:alice:secret:+vivify=foo", wanterr: `invalid basic auth syntax. got "alice:secret:+vivify=foo", want "username:password"`},
	}
	for _, tt := range tests {
		am, err := FromConfig(tt.in)
		if err != nil || tt.wanterr != nil {
			if fmt.Sprint(err) != fmt.Sprint(tt.wanterr) {
				t.Errorf("FromConfig(%q) = error %v; want %v", tt.in, err, tt.wanterr)
			}
			continue
		}
		if !reflect.DeepEqual(am, tt.want) {
			t.Errorf("FromConfig(%q) = %#v; want %#v", tt.in, am, tt.want)
		}
	}
}

func TestMultiMode(t *testing.T) {
	req, err := http.NewRequest("GET", "/", nil)
	if err != nil {
		t.Fatal(err)
	}
	(&UserPass{Username: "foo", Password: "bar"}).AddAuthHeader(req)

	modes = []AuthMode{
		&UserPass{
			Username: "foo",
			Password: "baz",
		},
	}

	if Allowed(req, OpAll) == true {
		t.Fatalf("req should not be allowed")
	}

	AddMode(&UserPass{
		Username: "foo",
		Password: "bar",
	})

	if Allowed(req, OpAll) == false {
		t.Fatalf("req should now be allowed")
	}

	SetMode(&UserPass{
		Username: "foo",
		Password: "baz",
	})

	if Allowed(req, OpAll) == true {
		t.Fatalf("req should not be allowed anymore")
	}

}
