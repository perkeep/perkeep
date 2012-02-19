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

// Package jsonconfig defines a helper type for JSON objects to be
// used for configuration.
package jsonconfig

import (
	"fmt"
	"strings"
)

// Obj is a JSON configuration map.
type Obj map[string]interface{}

// Reads json config data from the specified open file, expanding
// all expressions 
func ReadFile(configPath string) (Obj, error) {
	var c configParser
	var err error
	c.touchedFiles = make(map[string]bool)
	c.RootJson, err = c.recursiveReadJSON(configPath)
	return c.RootJson, err
}

func (jc Obj) RequiredObject(key string) Obj {
	return jc.obj(key, false)
}

func (jc Obj) OptionalObject(key string) Obj {
	return jc.obj(key, true)
}

func (jc Obj) obj(key string, optional bool) Obj {
	jc.noteKnownKey(key)
	ei, ok := jc[key]
	if !ok {
		if optional {
			return make(Obj)
		}
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
	return jc.string(key, nil)
}

func (jc Obj) OptionalString(key, def string) string {
	return jc.string(key, &def)
}

func (jc Obj) string(key string, def *string) string {
	jc.noteKnownKey(key)
	ei, ok := jc[key]
	if !ok {
		if def != nil {
			return *def
		}
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

func (jc Obj) RequiredStringOrObject(key string) interface{} {
	return jc.stringOrObject(key, true)
}

func (jc Obj) OptionalStringOrObject(key string) interface{} {
	return jc.stringOrObject(key, false)
}

func (jc Obj) stringOrObject(key string, required bool) interface{} {
	jc.noteKnownKey(key)
	ei, ok := jc[key]
	if !ok {
		if !required {
			return nil
		}
		jc.appendError(fmt.Errorf("Missing required config key %q (string or object)", key))
		return ""
	}
	if _, ok := ei.(map[string]interface{}); ok {
		return ei
	}
	if _, ok := ei.(string); ok {
		return ei
	}
	jc.appendError(fmt.Errorf("Expected config key %q to be a string or object", key))
	return ""
}

func (jc Obj) RequiredBool(key string) bool {
	return jc.bool(key, nil)
}

func (jc Obj) OptionalBool(key string, def bool) bool {
	return jc.bool(key, &def)
}

func (jc Obj) bool(key string, def *bool) bool {
	jc.noteKnownKey(key)
	ei, ok := jc[key]
	if !ok {
		if def != nil {
			return *def
		}
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

func (jc Obj) RequiredInt(key string) int {
	return jc.int(key, nil)
}

func (jc Obj) OptionalInt(key string, def int) int {
	return jc.int(key, &def)
}

func (jc Obj) int(key string, def *int) int {
	jc.noteKnownKey(key)
	ei, ok := jc[key]
	if !ok {
		if def != nil {
			return *def
		}
		jc.appendError(fmt.Errorf("Missing required config key %q (integer)", key))
		return 0
	}
	b, ok := ei.(float64)
	if !ok {
		jc.appendError(fmt.Errorf("Expected config key %q to be a number", key))
		return 0
	}
	return int(b)
}

func (jc Obj) RequiredList(key string) []string {
	return jc.requiredList(key, true)
}

func (jc Obj) OptionalList(key string) []string {
	return jc.requiredList(key, false)
}

func (jc Obj) requiredList(key string, required bool) []string {
	jc.noteKnownKey(key)
	ei, ok := jc[key]
	if !ok {
		if required {
			jc.appendError(fmt.Errorf("Missing required config key %q (list of strings)", key))
		}
		return nil
	}
	eil, ok := ei.([]interface{})
	if !ok {
		jc.appendError(fmt.Errorf("Expected config key %q to be a list, not %T", key, ei))
		return nil
	}
	sl := make([]string, len(eil))
	for i, ei := range eil {
		s, ok := ei.(string)
		if !ok {
			jc.appendError(fmt.Errorf("Expected config key %q index %d to be a string, not %T", key, i, ei))
			return nil
		}
		sl[i] = s
	}
	return sl
}

func (jc Obj) noteKnownKey(key string) {
	_, ok := jc["_knownkeys"]
	if !ok {
		jc["_knownkeys"] = make(map[string]bool)
	}
	jc["_knownkeys"].(map[string]bool)[key] = true
}

func (jc Obj) appendError(err error) {
	ei, ok := jc["_errors"]
	if ok {
		jc["_errors"] = append(ei.([]error), err)
	} else {
		jc["_errors"] = []error{err}
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

func (jc Obj) Validate() error {
	jc.lookForUnknownKeys()

	ei, ok := jc["_errors"]
	if !ok {
		return nil
	}
	errList := ei.([]error)
	if len(errList) == 1 {
		return errList[0]
	}
	strs := make([]string, 0)
	for _, v := range errList {
		strs = append(strs, v.Error())
	}
	return fmt.Errorf("Multiple errors: " + strings.Join(strs, ", "))
}
