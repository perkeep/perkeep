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

package dbimpl

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
)

type boolType struct{}

var Bool = boolType{}

func (boolType) ConvertValue(v interface{}) (interface{}, os.Error) {
	return nil, fmt.Errorf("TODO(bradfitz): bool conversions")
}

type int32Type struct{}

var Int32 = int32Type{}

func (int32Type) ConvertValue(v interface{}) (interface{}, os.Error) {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		i64 := rv.Int()
		if i64 > (1<<31)-1 || i64 < -(1<<31) {
			return nil, fmt.Errorf("value %d overflows int32", v)
		}
		return int32(i64), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		u64 := rv.Uint()
		if u64 > (1<<31)-1 {
			return nil, fmt.Errorf("value %d overflows int32", v)
		}
		return int32(u64), nil
	case reflect.String:
		i, err := strconv.Atoi(rv.String())
		if err != nil {
			return nil, fmt.Errorf("value %q can't be converted to int32", v)
		}
		return int32(i), nil
	}
	return nil, fmt.Errorf("unsupported value %v (type %T) converting to int32", v, v)
}

type stringType struct{}

var String = stringType{}

func (stringType) ConvertValue(v interface{}) (interface{}, os.Error) {
	if s, ok := v.(string); ok {
		return s, nil
	}
	return fmt.Sprintf("%v", v), nil
}

// SubsetValue converts v to one of the restricted subset types that
// dbimpl drivers need to support: int64, float64, bool, nil, []byte
func SubsetValue(v interface{}) (interface{}, os.Error) {
	if v == nil {
		return nil, nil
	}
	if _, ok := v.([]byte); ok {
		return v, nil
	}
	if _, ok := v.(bool); ok {
		return v, nil
	}
	if s, ok := v.(string); ok {
		return []byte(s), nil
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32:
		return int64(rv.Uint()), nil
	case reflect.Uint64:
		u64 := rv.Uint()
		if u64 >= 1<<63 {
			return nil, fmt.Errorf("uint64 values with high bit set are not supported")
		}
		return int64(u64), nil
	case reflect.Float32, reflect.Float64:
		return rv.Float(), nil
	}
	return nil, fmt.Errorf("unsupported type %s", rv.Kind())
}
