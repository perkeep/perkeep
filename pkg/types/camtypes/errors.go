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

package camtypes

import (
	"fmt"
)

//TODO(mpl): move pkg/camerrors stuff in here

var camErrors = map[string]error{}

func init() {
	// TODO(mpl): set des to be "See http://camlistore.org/err/client-no-public-key"
	addCamError("client-no-public-key", "No public key configured: see 'camput init'.")
}

type camErr struct {
	key string
	des string // full error description
}

func (ce *camErr) Error() string {
	return ce.des
}

// Err returns the error registered for key.
// It panics for an unregistered key.
func Err(key string) error {
	v, ok := camErrors[key]
	if !ok {
		panic(fmt.Sprintf("unknown/unregistered error key %v", key))
	}
	return v
}

func addCamError(key, des string) {
	camErrors[key] = &camErr{
		key: key,
		des: des,
	}
}
