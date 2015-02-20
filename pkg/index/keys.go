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
	"strings"

	"camlistore.org/pkg/blob"
)

// requiredSchemaVersion is incremented every time
// an index key type is added, changed, or removed.
// Version 4: EXIF tags + GPS
// Version 5: wholeRef added to keyFileInfo
const requiredSchemaVersion = 5

// type of key returns the identifier in k before the first ":" or "|".
// (Originally we packed keys by hand and there are a mix of styles)
func typeOfKey(k string) string {
	c := strings.Index(k, ":")
	p := strings.Index(k, "|")
	if c < 0 && p < 0 {
		return ""
	}
	if c < 0 {
		return k[:p]
	}
	if p < 0 {
		return k[:c]
	}
	min := c
	if p < min {
		min = p
	}
	return k[:min]
}

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
		case typeIntStr:
			switch arg.(type) {
			case int, int64, uint64:
				buf.WriteString(fmt.Sprintf("%d", arg))
			default:
				panic("bogus int type")
			}
		case typeStr:
			buf.WriteString(urle(asStr()))
		case typeRawStr:
			buf.WriteString(asStr())
		case typeReverseTime:
			s := asStr()
			const example = "2011-01-23T05:23:12"
			if len(s) < len(example) || s[4] != '-' && s[10] != 'T' {
				panic("doesn't look like a time: " + s)
			}
			buf.WriteString(reverseTimeString(s))
		case typeBlobRef:
			if br, ok := arg.(blob.Ref); ok {
				if br.Valid() {
					buf.WriteString(br.String())
				}
				break
			}
			fallthrough
		default:
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
	typeStr    // URL-escaped
	typeIntStr // integer as string
	typeRawStr // not URL-escaped
)

var (
	// keySchemaVersion indexes the index schema version.
	keySchemaVersion = &keyType{
		"schemaversion",
		nil,
		[]part{
			{"version", typeIntStr},
		},
	}

	keyMissing = &keyType{
		"missing",
		[]part{
			{"have", typeBlobRef},
			{"needed", typeBlobRef},
		},
		[]part{
			{"1", typeStr},
		},
	}

	// keyPermanodeClaim indexes when a permanode is modified (or deleted) by a claim.
	// It ties the affected permanode to the date of the modification, the responsible
	// claim, and the nature of the modification.
	keyPermanodeClaim = &keyType{
		"claim",
		[]part{
			{"permanode", typeBlobRef}, // modified permanode
			{"signer", typeKeyId},
			{"claimDate", typeTime},
			{"claim", typeBlobRef},
		},
		[]part{
			{"claimType", typeStr},
			{"attr", typeStr},
			{"value", typeStr},
			// And the signerRef, which seems redundant
			// with the signer keyId in the jey, but the
			// Claim struct needs this, and there's 1:m
			// for keyId:blobRef, so:
			{"signerRef", typeBlobRef},
		},
	}

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

	keyFileInfo = &keyType{
		"fileinfo",
		[]part{
			{"file", typeBlobRef},
		},
		[]part{
			{"size", typeIntStr},
			{"filename", typeStr},
			{"mimetype", typeStr},
			{"whole", typeBlobRef},
		},
	}

	keyFileTimes = &keyType{
		"filetimes",
		[]part{
			{"file", typeBlobRef},
		},
		[]part{
			// 0, 1, or 2 comma-separated types.Time3339
			// strings for creation/mod times. Oldest,
			// then newest. See FileInfo docs.
			{"time3339s", typeStr},
		},
	}

	keySignerAttrValue = &keyType{
		"signerattrvalue",
		[]part{
			{"signer", typeKeyId},
			{"attr", typeStr},
			{"value", typeStr},
			{"claimdate", typeReverseTime},
			{"claimref", typeBlobRef},
		},
		[]part{
			{"permanode", typeBlobRef},
		},
	}

	// keyDeleted indexes a claim that deletes an entity. It ties the deleted
	// entity to the date it was deleted, and to the deleter claim.
	keyDeleted = &keyType{
		"deleted",
		[]part{
			{"deleted", typeBlobRef}, // the deleted entity (a permanode or another claim)
			{"claimdate", typeReverseTime},
			{"deleter", typeBlobRef}, // the deleter claim blobref
		},
		nil,
	}

	// Given a blobref (permanode or static file or directory), provide a mapping
	// to potential parents (they may no longer be parents, in the case of permanodes).
	// In the case of permanodes, camliMember or camliContent constitutes a forward
	// edge.  In the case of static directories, the forward path is dir->static set->file,
	// and that's what's indexed here, inverted.
	keyEdgeBackward = &keyType{
		"edgeback",
		[]part{
			{"child", typeBlobRef},  // the edge target; thing we want to find parent(s) of
			{"parent", typeBlobRef}, // the parent / edge source (e.g. permanode blobref)
			// the blobref is the blob establishing the relationship
			// (for a permanode: the claim; for static: often same as parent)
			{"blobref", typeBlobRef},
		},
		[]part{
			{"parenttype", typeStr}, // either "permanode" or the camliType ("file", "static-set", etc)
			{"name", typeStr},       // the name, if static.
		},
	}

	// Width and height after any EXIF rotation.
	keyImageSize = &keyType{
		"imagesize",
		[]part{
			{"fileref", typeBlobRef}, // blobref of "file" schema blob
		},
		[]part{
			{"width", typeStr},
			{"height", typeStr},
		},
	}

	// child of a directory
	keyStaticDirChild = &keyType{
		"dirchild",
		[]part{
			{"dirref", typeBlobRef}, // blobref of "directory" schema blob
			{"child", typeStr},      // blobref of the child
		},
		[]part{
			{"1", typeStr},
		},
	}

	// Media attributes (e.g. ID3 tags). Uses generic terms like
	// "artist", "title", "album", etc.
	keyMediaTag = &keyType{
		"mediatag",
		[]part{
			{"wholeRef", typeBlobRef}, // wholeRef for song
			{"tag", typeStr},
		},
		[]part{
			{"value", typeStr},
		},
	}

	// EXIF tags
	keyEXIFTag = &keyType{
		"exiftag",
		[]part{
			{"wholeRef", typeBlobRef}, // of entire file, not fileref
			{"tag", typeStr},          // uint16 tag number as hex: xxxx
		},
		[]part{
			{"type", typeStr},    // "int", "rat", "float", "string"
			{"n", typeIntStr},    // n components of type
			{"vals", typeRawStr}, // pipe-separated; rats are n/d. strings are URL-escaped.
		},
	}

	// Redundant version of keyEXIFTag. TODO: maybe get rid of this.
	// Easier to process as one row instead of 4, though.
	keyEXIFGPS = &keyType{
		"exifgps",
		[]part{
			{"wholeRef", typeBlobRef}, // of entire file, not fileref
		},
		[]part{
			{"lat", typeStr},
			{"long", typeStr},
		},
	}
)

func containsUnsafeRawStrByte(s string) bool {
	for _, r := range s {
		if r >= 'z' || r < ' ' {
			// pipe ('|) and non-ASCII are above 'z'.
			return true
		}
		if r == '%' || r == '+' {
			// Could be interpretted as URL-encoded
			return true
		}
	}
	return false
}
