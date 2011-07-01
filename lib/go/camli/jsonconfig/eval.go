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
	"container/vector"
	"fmt"
	"json"
	"log"
	"os"
	"path/filepath"
	"regexp"

	"camli/errorutil"
	"camli/osutil"
)


// State for config parsing and expression evalutaion
type configParser struct {
	RootJson     Obj

	touchedFiles map[string]bool
	includeStack vector.StringVector
}

// Validates variable names for config _env expresssions
var envPattern = regexp.MustCompile(`\$\{[A-Za-z0-9_]+\}`)

// Decodes and evaluates a json config file, watching for include cycles.
func (c *configParser) recursiveReadJson(configPath string) (decodedObject map[string]interface{}, err os.Error) {

	configPath, err = filepath.Abs(configPath)
	if err != nil {
		return nil, fmt.Errorf("Failed to expand absolute path for %s", configPath)
	}
	if c.touchedFiles[configPath] {
		return nil, fmt.Errorf("configParser include cycle detected reading config: %v",
			configPath)
	}
	c.touchedFiles[configPath] = true

	c.includeStack.Push(configPath)
	defer c.includeStack.Pop()

	var f *os.File
	if f, err = os.Open(configPath); err != nil {
		return nil, fmt.Errorf("Failed to open config: %s, %v", configPath, err)
	}
	defer f.Close()

	decodedObject = make(map[string]interface{})
	dj := json.NewDecoder(f)
	if err = dj.Decode(&decodedObject); err != nil {
		extra := ""
		if serr, ok := err.(*json.SyntaxError); ok {
			if _, serr := f.Seek(0, os.SEEK_SET); serr != nil {
				log.Fatalf("seek error: %v", serr)
			}
			line, col, highlight := errorutil.HighlightBytePosition(f, serr.Offset)
			extra = fmt.Sprintf(":\nError at line %d, column %d (file offset %d):\n%s",
				line, col, serr.Offset, highlight)
		}
		return nil, fmt.Errorf("error parsing JSON object in config file %s%s\n%v",
			f.Name(), extra, err)
	}

	if err = c.evaluateExpressions(decodedObject); err != nil {
		return nil, fmt.Errorf("error expanding JSON config expressions in %s:\n%v",
			f.Name(), err)
	}

	return decodedObject, nil
}

func (c *configParser) evaluateExpressions(m map[string]interface{}) os.Error {
	for k, ei := range m {
		switch subval := ei.(type) {
		case string:
			continue
		case bool:
			continue
		case float64:
			continue
		case []interface{}:
			if len(subval) == 0 {
				continue
			}
			var expander func(c *configParser, v []interface{}) (interface{}, os.Error)
			if firstString, ok := subval[0].(string); ok {
				switch firstString {
				case "_env":
					expander = (*configParser).expandEnv
				case "_fileobj":
					expander = (*configParser).expandFile
				}
			}
			if expander != nil {
				newval, err := expander(c, subval[1:])
				if err != nil {
					return err
				}
				m[k] = newval
			}
		case map[string]interface{}:
			if err := c.evaluateExpressions(subval); err != nil {
				return err
			}
		default:
			return fmt.Errorf("Unhandled type %T", ei)
		}
	}
	return nil
}

// Permit either:
//    ["_env", "VARIABLE"] (required to be set)
// or ["_env", "VARIABLE", "default_value"]
func (c *configParser) expandEnv(v []interface{}) (interface{}, os.Error) {
	hasDefault := false
	def := ""
	if len(v) < 1 || len(v) > 2 {
		return "", fmt.Errorf("_env expansion expected 1 or 2 args, got %d", len(v))
	}
	s, ok := v[0].(string)
	if !ok {
		return "", fmt.Errorf("Expected a string after _env expansion; got %#v", v[0])
	}
	if len(v) == 2 {
		hasDefault = true
		def, hasDefault = v[1].(string)
		if !hasDefault {
			return "", fmt.Errorf("Expected default value in %q _env expansion; got %#v", s, v[1])
		}
	}
	var err os.Error
	expanded := envPattern.ReplaceAllStringFunc(s, func(match string) string {
		envVar := match[2 : len(match)-1]
		val := os.Getenv(envVar)
		if val == "" {
			if hasDefault {
				return def
			}
			err = fmt.Errorf("couldn't expand environment variable %q", envVar)
		}
		return val
	})
	return expanded, err
}

func (c *configParser) expandFile(v []interface{}) (exp interface{}, err os.Error) {
	if len(v) != 1 {
		return "", fmt.Errorf("_file expansion expected 1 arg, got %d", len(v))
	}
	var incPath string
	if incPath, err = osutil.FindCamliInclude(v[0].(string)); err != nil {
		return "", fmt.Errorf("Included config does not exist: %v", v[0])
	}
	if exp, err = c.recursiveReadJson(incPath); err != nil {
		return "", fmt.Errorf("In file included from %s:\n%v",
			c.includeStack.Last(), err)
	}
	return exp, nil
}

