/*
Copyright 2013 The Camlistore Authors.

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

// Package camerrors define specific errors that are used to
// decide on how to deal with some failure cases.
package camerrors

import (
	"errors"
)

// ErrMissingKeyBlob is returned by the jsonsign handler when a
// verification fails because the public key for a signed blob is
// missing.
var ErrMissingKeyBlob = errors.New("key blob not found")
