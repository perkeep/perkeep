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

package jsonconfig

import (
	"fmt"
	"os"
	"strings"
)

type Obj map[string]interface{}

func (jc Obj) RequiredObject(key string) Obj {
	jc.noteKnownKey(key)
        ei, ok := jc[key]
	if !ok {
		jc.appendError(fmt.Errorf("Missing required config key %q (object)", key))
		return make(Obj)
	}
	m, ok := ei.(map[string]interface{})
	if !ok {
		jc.appendError(fmt.Errorf("Expected config key %q to be an object, not %T", key, ei))
		return make(Obj)
	}
	return Obj(m)
}

func (jc Obj) RequiredString(key string) string {
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

func (jc Obj) OptionalString(key, def string) string {
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

func (jc Obj) RequiredBool(key string) bool {
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

func (jc Obj) OptionalBool(key string, def bool) bool {
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

func (jc Obj) noteKnownKey(key string) {
	_, ok := jc["_knownkeys"]
	if !ok {
                jc["_knownkeys"] = make(map[string]bool)
	}
	jc["_knownkeys"].(map[string]bool)[key] = true
}

func (jc Obj) appendError(err os.Error) {
	ei, ok := jc["_errors"]
	if ok {
		jc["_errors"] = append(ei.([]os.Error), err)
	} else {
		jc["_errors"] = []os.Error{err}
	}
}

func (jc Obj) lookForUnknownKeys() {
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

func (jc Obj) Validate() os.Error {
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
