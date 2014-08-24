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

package main

import (
	"errors"
	"flag"
	"log"
	"os"
	"path/filepath"
	"strings"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/jsonsign"
	"camlistore.org/pkg/osutil"
)

const (
	// default secret ring used in tests and in devcam commands
	defaultSecring = "pkg/jsonsign/testdata/test-secring.gpg"
	// public ID of the GPG key in defaultSecring
	defaultIdentity = "26F5ABDA"
)

var (
	flagSecretRing = flag.String("secretring", "", "the secret ring file to run with")
	flagIdentity   = flag.String("identity", "", "the key id of the identity to run with")
)

type Env struct {
	m     map[string]string
	order []string
}

func (e *Env) Set(k, v string) {
	_, dup := e.m[k]
	e.m[k] = v
	if !dup {
		e.order = append(e.order, k)
	}
}

func (e *Env) Del(k string) {
	delete(e.m, k)
}

// NoGo removes GOPATH and GOBIN.
func (e *Env) NoGo() {
	e.Del("GOPATH")
	e.Del("GOBIN")
}

func (e *Env) Flat() []string {
	vv := make([]string, 0, len(e.order))
	for _, k := range e.order {
		if v, ok := e.m[k]; ok {
			vv = append(vv, k+"="+v)
		}
	}
	return vv
}

func NewEnv() *Env {
	return &Env{make(map[string]string), nil}
}

func NewCopyEnv() *Env {
	env := NewEnv()
	for _, kv := range os.Environ() {
		eq := strings.Index(kv, "=")
		if eq > 0 {
			env.Set(kv[:eq], kv[eq+1:])
		}
	}
	return env
}

func (e *Env) SetCamdevVars(altkey bool) {
	setCamdevVarsFor(e, altkey)
}

func setCamdevVars() {
	setCamdevVarsFor(nil, false)
}

func rootInTmpDir() (string, error) {
	user := osutil.Username()
	if user == "" {
		return "", errors.New("Could not get username from environment")
	}
	return filepath.Join(os.TempDir(), "camliroot-"+user), nil
}

func setCamdevVarsFor(e *Env, altkey bool) {
	var setenv func(string, string) error
	if e != nil {
		setenv = func(k, v string) error { e.Set(k, v); return nil }
	} else {
		setenv = os.Setenv
	}

	setenv("CAMLI_AUTH", "userpass:camlistore:pass3179")
	// env values for clients. server will overwrite them anyway in its setEnvVars.
	root, err := rootInTmpDir()
	if err != nil {
		log.Fatal(err)
	}
	setenv("CAMLI_CACHE_DIR", filepath.Join(root, "client", "cache"))
	setenv("CAMLI_CONFIG_DIR", filepath.Join("config", "dev-client-dir"))

	secring := defaultSecring
	identity := defaultIdentity

	if altkey {
		secring = filepath.FromSlash("pkg/jsonsign/testdata/password-foo-secring.gpg")
		identity = "C7C3E176"
		println("**\n** Note: password is \"foo\"\n**\n")
	} else {
		if *flagSecretRing != "" {
			secring = *flagSecretRing
		}
		if *flagIdentity != "" {
			identity = *flagIdentity
		}
	}

	entity, err := jsonsign.EntityFromSecring(identity, secring)
	if err != nil {
		panic(err)
	}
	armoredPublicKey, err := jsonsign.ArmoredPublicKey(entity)
	if err != nil {
		panic(err)
	}
	pubKeyRef := blob.SHA1FromString(armoredPublicKey)

	setenv("CAMLI_SECRET_RING", secring)
	setenv("CAMLI_KEYID", identity)
	setenv("CAMLI_PUBKEY_BLOBREF", pubKeyRef.String())
	setenv("CAMLI_KV_VERIFY", "true")
}

func (e *Env) wipeCacheDir() {
	cacheDir, _ := e.m["CAMLI_CACHE_DIR"]
	if cacheDir == "" {
		log.Fatal("Could not wipe cache dir, CAMLI_CACHE_DIR not defined")
	}
	if err := os.RemoveAll(cacheDir); err != nil {
		log.Fatalf("Could not remove cache dir %v: %v", cacheDir, err)
	}
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		log.Fatalf("Could not recreate cache dir %v: %v", cacheDir, err)
	}
}
