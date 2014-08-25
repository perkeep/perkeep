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
	"log"

	"camlistore.org/pkg/osutil"
)

// TODO(mpl): move pkg/camerrors stuff in here

var camErrors = map[string]*camErr{}

var (
	ErrClientNoServer = addCamError("client-no-server", funcStr(func() string {
		return fmt.Sprintf("No valid server defined. It can be set with the CAMLI_SERVER environment variable, or the --server flag, or in the \"servers\" section of %q (see https://camlistore.org/docs/client-config).", osutil.UserClientConfigPath())
	}))
	ErrClientNoPublicKey = addCamError("client-no-public-key", str("No public key configured: see 'camput init'."))
)

type str string

func (s str) String() string { return string(s) }

type funcStr func() string

func (f funcStr) String() string { return f() }

type camErr struct {
	key string
	des fmt.Stringer
}

func (ce *camErr) Error() string {
	return ce.des.String()
}

func (ce *camErr) Fatal() {
	log.Fatalf("%v error. See %v", ce.key, ce.URL())
}

func (ce *camErr) Warn() {
	log.Printf("%v error. See %v.", ce.key, ce.URL())
}

func (ce *camErr) URL() string {
	return fmt.Sprintf("https://camlistore.org/err/%s", ce.key)
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

func addCamError(key string, des fmt.Stringer) *camErr {
	if e, ok := camErrors[key]; ok {
		panic(fmt.Sprintf("error %v already registered as %q", key, e.Error()))
	}
	e := &camErr{
		key: key,
		des: des,
	}
	camErrors[key] = e
	return e
}
