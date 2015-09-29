/*
Copyright 2015 The Camlistore Authors

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

package conv

import (
	"reflect"
	"strings"
	"testing"

	"camlistore.org/pkg/blob"
)

func TestParseFields(t *testing.T) {
	tests := []struct {
		in   string
		want []interface{}
		err  string
	}{
		{in: "5 17", want: []interface{}{uint64(5), uint32(17)}},
		{in: "1", want: []interface{}{uint64(1)}},
		{in: "1", want: []interface{}{int64(1)}},
		{in: "5 sha1-0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33 8",
			want: []interface{}{
				int64(5),
				blob.MustParse("sha1-0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33"),
				uint32(8),
			},
		},
		{in: "-5", want: []interface{}{int64(-5)}, err: "invalid syntax"},
	}

	for i, tt := range tests {
		var gotp []interface{}
		var gotrv []reflect.Value
		for _, wantv := range tt.want {
			rv := reflect.New(reflect.TypeOf(wantv))
			gotrv = append(gotrv, rv)
			gotp = append(gotp, rv.Interface())
		}
		gotErr := ParseFields([]byte(tt.in), gotp...)
		if gotErr != nil && tt.err != "" {
			if strings.Contains(gotErr.Error(), tt.err) {
				continue
			}
			t.Errorf("%d. error = %v; want substring %q", i, gotErr, tt.err)
			continue
		}
		if (gotErr != nil) != (tt.err != "") {
			t.Errorf("%d. error = %v; want substring %q", i, gotErr, tt.err)
			continue
		}
		var got []interface{}
		for _, rv := range gotrv {
			got = append(got, rv.Elem().Interface())
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%d. got = %#v; want %#v", i, got, tt.want)
		}
	}
}
