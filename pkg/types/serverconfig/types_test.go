/*
Copyright 2013 Google Inc.

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
