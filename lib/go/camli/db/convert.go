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

// Type conversions for Scan.

package db

import (
	"fmt"
	"os"
	"reflect"
)

// copyConvert copies to dest the value in src, converting it if possible
// An error is returned if the copy would result in loss of information.
// dest should be a pointer type.
func copyConvert(dest, src interface{}) os.Error {
	dpv := reflect.ValueOf(dest)
	if dpv.Kind() != reflect.Ptr {
		return os.NewError("destination not a pointer")
	}

	switch s := src.(type) {
	case []byte:
		switch d := dest.(type) {
		case *string:
			*d = string(s)
			return nil
		case *[]byte:
			*d = s
			return nil
		}
	}

	dv := reflect.Indirect(dpv)
	sv := reflect.ValueOf(src)
	if dv.Kind() == sv.Kind() {
		dv.Set(sv)
		return nil
	}

	return fmt.Errorf("unsupported driver -> Scan pair: %T -> %T", src, dest)
}