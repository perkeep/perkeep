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

package blobserver

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

type JSONConfig map[string]interface{}

func (jc JSONConfig) RequiredString(key string) string {
	jc.noteKnownKey(key)
	ei, ok := jc[key]
	if !ok {
		jc.appendError(fmt.Errorf("Missing required config key %q (string)", key))
		return ""
	}
	s, ok := ei.(string)
	if !ok {
		jc.appendError(fmt.Errorf("Expected config key %q to be a string", key))
		return ""
	}
	return s
}

func (jc JSONConfig) OptionalString(key, def string) string {
	jc.noteKnownKey(key)
	ei, ok := jc[key]
	if !ok {
		return def
	}
	s, ok := ei.(string)
	if !ok {
		jc.appendError(fmt.Errorf("Expected config key %q to be a string", key))
		return ""
	}
	return s
}

func (jc JSONConfig) RequiredBool(key string) bool {
	jc.noteKnownKey(key)
	ei, ok := jc[key]
	if !ok {
		jc.appendError(fmt.Errorf("Missing required config key %q (boolean)", key))
		return false
	}
	b, ok := ei.(bool)
	if !ok {
		jc.appendError(fmt.Errorf("Expected config key %q to be a boolean", key))
		return false
	}
	return b
}

func (jc JSONConfig) OptionalBool(key string, def bool) bool {
	jc.noteKnownKey(key)
	ei, ok := jc[key]
	if !ok {
		return def
	}
	b, ok := ei.(bool)
	if !ok {
		jc.appendError(fmt.Errorf("Expected config key %q to be a boolean", key))
		return def
	}
	return b
}

func (jc JSONConfig) noteKnownKey(key string) {
	_, ok := jc["_knownkeys"]
	if !ok {
                jc["_knownkeys"] = make(map[string]bool)
	}
	jc["_knownkeys"].(map[string]bool)[key] = true
}

func (jc JSONConfig) appendError(err os.Error) {
	ei, ok := jc["_errors"]
	if ok {
		jc["_errors"] = append(ei.([]os.Error), err)
	} else {
		jc["_errors"] = []os.Error{err}
	}
}

func (jc JSONConfig) lookForUnknownKeys() {
	ei, ok := jc["_knownkeys"]
	var known map[string]bool
	if ok {
		known = ei.(map[string]bool)
	}
	for k, _ := range jc {
		if ok && known[k] {
			continue
		}
		if strings.HasPrefix(k, "_") {
			// Permit keys with a leading underscore as a
			// form of comments.
			continue
		}
		jc.appendError(fmt.Errorf("Unknown key %q", k))
	}
}

func (jc JSONConfig) Validate() os.Error {
	jc.lookForUnknownKeys()

	ei, ok := jc["_errors"]
	if !ok {
		return nil
	}
	errList := ei.([]os.Error)
	if len(errList) == 1 {
		return errList[0]
	}
	strs := make([]string, 0)
	for _, v := range errList {
		strs = append(strs, v.String())
	}
	return fmt.Errorf("Multiple errors: " + strings.Join(strs, ", "))
}

type StorageConstructor func(config JSONConfig) (Storage, os.Error)

var mapLock sync.Mutex
var storageConstructors = make(map[string]StorageConstructor)

func RegisterStorageConstructor(typ string, ctor StorageConstructor) {
	mapLock.Lock()
	defer mapLock.Unlock()
	if _, ok := storageConstructors[typ]; ok {
		panic("blobserver: StorageConstructor already registered for type: " + typ)
	}
	storageConstructors[typ] = ctor
}

func CreateStorage(typ string, config JSONConfig) (Storage, os.Error) {
	mapLock.Lock()
	ctor, ok := storageConstructors[typ]
	mapLock.Unlock()
	if !ok {
		return nil, fmt.Errorf("Storage type %q not known or loaded", typ)
	}
	return ctor(config)
}
