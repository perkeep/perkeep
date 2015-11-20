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

// Package conv contains utilities for parsing values delimited by spaces.
package conv

import (
	"bytes"
	"errors"
	"fmt"

	"camlistore.org/pkg/blob"

	"go4.org/strutil"
)

func ParseFields(v []byte, dst ...interface{}) error {
	for i, dv := range dst {
		thisv := v
		if i < len(dst)-1 {
			sp := bytes.IndexByte(v, ' ')
			if sp == -1 {
				return fmt.Errorf("missing space following field index %d", i)
			}
			thisv = v[:sp]
			v = v[sp+1:]
		}
		switch dv := dv.(type) {
		case *blob.Ref:
			br, ok := blob.ParseBytes(thisv)
			if !ok {

			}
			*dv = br
		case *uint32:
			n, err := strutil.ParseUintBytes(thisv, 10, 32)
			if err != nil {
				return err
			}
			*dv = uint32(n)
		case *uint64:
			n, err := strutil.ParseUintBytes(thisv, 10, 64)
			if err != nil {
				return err
			}
			*dv = n
		case *int64:
			n, err := strutil.ParseUintBytes(thisv, 10, 64)
			if err != nil {
				return err
			}
			if int64(n) < 0 {
				return errors.New("conv: negative numbers not accepted with int64 dest type")
			}
			*dv = int64(n)
		default:
			return fmt.Errorf("conv: unsupported target pointer type %T", dv)
		}
	}
	return nil
}
