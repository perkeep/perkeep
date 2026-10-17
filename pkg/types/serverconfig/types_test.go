/*
Copyright 2013 The Perkeep Authors

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

package serverconfig

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestInvertedBool_Unmarshal(t *testing.T) {
	tests := []struct {
		json string
		want bool
	}{
		{json: `{}`, want: true},
		{json: `{"key": true}`, want: true},
		{json: `{"key": false}`, want: false},
	}
	type O struct {
		Key invertedBool
	}
	for _, tt := range tests {
		obj := &O{}
		if err := json.Unmarshal([]byte(tt.json), obj); err != nil {
			t.Fatalf("Could not unmarshal %s: %v", tt.json, err)
		}
		if obj.Key.Get() != tt.want {
			t.Errorf("Unmarshaled %s as invertedBool; got %v, wanted %v", tt.json, obj.Key.Get(), tt.want)
		}
	}
}

func TestInvertedBool_Marshal(t *testing.T) {
	tests := []struct {
		internalVal bool
		want        string
	}{
		{internalVal: true, want: `{"key":false}`},
		{internalVal: false, want: `{"key":true}`},
	}
	type O struct {
		Key invertedBool `json:"key"`
	}
	for _, tt := range tests {

		obj := &O{
			Key: invertedBool(tt.internalVal),
		}
		b, err := json.Marshal(obj)
		if err != nil {
			t.Fatalf("Could not marshal %v: %v", tt.internalVal, err)
		}
		if string(b) != tt.want {
			t.Errorf("Marshaled invertedBool %v; got %v, wanted %v", tt.internalVal, string(b), tt.want)
		}
	}
}

func TestImmichConfigJSON(t *testing.T) {
	without, err := json.Marshal(&Config{})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(without, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["immich"]; ok {
		t.Error("default configuration includes immich")
	}

	for _, tt := range []struct {
		name       string
		standalone bool
		limit      int64
	}{
		{name: "normal Perkeep auth"},
		{name: "normal Perkeep auth with upload limit", limit: 32 << 30},
		{name: "standalone credentials", standalone: true},
		{name: "standalone credentials with upload limit", standalone: true, limit: 32 << 30},
	} {
		t.Run(tt.name, func(t *testing.T) {
			want := &Immich{
				MetadataFile:   "/data/immich.leveldb",
				MaxUploadBytes: tt.limit,
			}
			if tt.standalone {
				want.Email = "photos@example.com"
				want.Password = "photo-password"
			}
			data, err := json.Marshal(&Config{Immich: want})
			if err != nil {
				t.Fatal(err)
			}
			var got Config
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Immich, want) {
				t.Errorf("JSON roundtrip = %#v; want %#v", got.Immich, want)
			}
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			var options map[string]json.RawMessage
			if err := json.Unmarshal(fields["immich"], &options); err != nil {
				t.Fatal(err)
			}
			if _, ok := options["maxUploadBytes"]; ok != (tt.limit != 0) {
				t.Errorf("maxUploadBytes presence = %v; want %v", ok, tt.limit != 0)
			}
			for _, key := range []string{"email", "password"} {
				if _, ok := options[key]; ok != tt.standalone {
					t.Errorf("%s presence = %v; want %v", key, ok, tt.standalone)
				}
			}
		})
	}
}
