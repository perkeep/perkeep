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

package index

import (
	"bytes"
	"fmt"
)

type keyType struct {
	name  string
	parts []keyPart
}

func (k *keyType) Prefix(args ...interface{}) string {
	return k.build(true, args...)
}

func (k *keyType) Key(args ...interface{}) string {
	return k.build(false, args...)
}

func (k *keyType) build(finalPipe bool, args ...interface{}) string {
	var buf bytes.Buffer
	buf.WriteString(k.name)
	for i, arg := range args {
		buf.WriteString("|")
		switch k.parts[i].typ {
		case typeReverseTime:
			s, ok := arg.(string)
			if !ok {
				s = arg.(fmt.Stringer).String()
			}
			const example = "2011-01-23T05:23:12"
			if len(s) < len(example) || s[4] != '-' && s[10] != 'T' {
				panic("doesn't look like a time: " + s)
			}
			buf.WriteString(reverseTimeString(s))
		default:
			// TODO(bradfitz): reverse time and such
			if s, ok := arg.(string); ok {
				buf.WriteString(s)
			} else {
				buf.WriteString(arg.(fmt.Stringer).String())
		}
		}
	}
	if finalPipe {
		buf.WriteString("|")
	}
	return buf.String()
}

type keyPart struct {
	name string
	typ  partType
}

type partType int

const (
	typeKeyId partType = iota // PGP key id
	typeTime
	typeReverseTime // time prepended with "rt" + each numeric digit reversed from '9'
	typeBlobRef
)

var (
	keyRecentPermanode = &keyType{
		"recpn",
		[]keyPart{
			{"owner", typeKeyId},
			{"modtime", typeReverseTime},
			{"claim", typeBlobRef},
		},
	}
)
