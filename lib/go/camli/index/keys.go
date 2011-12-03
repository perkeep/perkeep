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
	name     string
	keyParts []part
	valParts []part
}

func (k *keyType) Prefix(args ...interface{}) string {
	return k.build(true, true, k.keyParts, args...)
}

func (k *keyType) Key(args ...interface{}) string {
	return k.build(false, true, k.keyParts, args...)
}

func (k *keyType) Val(args ...interface{}) string {
	return k.build(false, false, k.valParts, args...)
}

func (k *keyType) build(isPrefix, isKey bool, parts []part, args ...interface{}) string {
	var buf bytes.Buffer
	if isKey {
		buf.WriteString(k.name)
	}
	if !isPrefix && len(args) != len(parts) {
		panic("wrong number of arguments")
	}
	if len(args) > len(parts) {
		panic("too many arguments")
	}
	for i, arg := range args {
		if isKey || i > 0 {
			buf.WriteString("|")
		}
		asStr := func() string {
			s, ok := arg.(string)
			if !ok {
				s = arg.(fmt.Stringer).String()
			}
			return s
		}
		switch parts[i].typ {
		case typeStr:
			buf.WriteString(urle(asStr()))
		case typeReverseTime:
			s := asStr()
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
	if isPrefix {
		buf.WriteString("|")
	}
	return buf.String()
}

type part struct {
	name string
	typ  partType
}

type partType int

const (
	typeKeyId partType = iota // PGP key id
	typeTime
	typeReverseTime // time prepended with "rt" + each numeric digit reversed from '9'
	typeBlobRef
	typeStr
)

var (
	keyRecentPermanode = &keyType{
		"recpn",
		[]part{
			{"owner", typeKeyId},
			{"modtime", typeReverseTime},
			{"claim", typeBlobRef},
		},
		nil,
	}

	keyPathBackward = &keyType{
		"signertargetpath",
		[]part{
			{"signer", typeKeyId},
			{"target", typeBlobRef},
			{"claim", typeBlobRef}, // for key uniqueness
		},
		[]part{
			{"claimDate", typeTime},
			{"base", typeBlobRef},
			{"active", typeStr}, // 'Y', or 'N' for deleted
			{"suffix", typeStr},
		},
	}

	keyPathForward = &keyType{
		"path",
		[]part{
			{"signer", typeKeyId},
			{"base", typeBlobRef},
			{"suffix", typeStr},
			{"claimDate", typeReverseTime},
			{"claim", typeBlobRef}, // for key uniqueness
		},
		[]part{
			{"active", typeStr}, // 'Y', or 'N' for deleted
			{"target", typeBlobRef},
		},
	}

	keyWholeToFileRef = &keyType{
		"wholetofile",
		[]part{
			{"whole", typeBlobRef},
			{"schema", typeBlobRef}, // for key uniqueness
		},
		[]part{
			{"1", typeStr},
		},
	}
)
