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
	"os"
	"fmt"
	"regexp"
)

var envPattern = regexp.MustCompile(`\$\{[A-Za-z0-9_]+\}`)

func EvaluateExpressions(m map[string]interface{}) os.Error {
	for k, ei := range m {
		switch subval := ei.(type) {
		case string:
			continue
		case []interface{}:
			if len(subval) == 0 {
				continue
			}
			var expander func(v []interface{}) (interface{}, os.Error)
			if firstString, ok := subval[0].(string); ok {
				switch firstString {
				case "_env":
					expander = expandEnv
				case "_file":
					expander = expandFile
				}
			}
			if expander != nil {
				newval, err := expander(subval[1:])
				if err != nil {
					return err
				}
				m[k] = newval
			}
		case map[string]interface{}:
			if err := EvaluateExpressions(subval); err != nil {
				return err
			}
		default:
			return fmt.Errorf("Unhandled type %T", ei)
		}
	}
	return nil
}

func expandEnv(v []interface{}) (interface{}, os.Error) {
	if len(v) != 1 {
		return "", fmt.Errorf("_env expansion expected 1 arg, got %d", len(v))
	}
	s, ok := v[0].(string)
	if !ok {
		return "", fmt.Errorf("Expected a string after _env expansion; got %#v", v)
	}
	var err os.Error
	expanded := envPattern.ReplaceAllStringFunc(s, func(match string) string {
		envVar := match[2 : len(match)-1]
		val := os.Getenv(envVar)
		if val == "" {
			err = fmt.Errorf("couldn't expand environment variable %q", envVar)
		}
		return val
	})
	return expanded, err
}

func expandFile(v []interface{}) (interface{}, os.Error) {
	return "", os.NewError("_file not implemented")
}
