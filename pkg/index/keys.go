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

// requiredSchemaVersion is incremented every time
// an index key type is added, changed, or removed.
const requiredSchemaVersion = 1

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
		case typeReverseTime:
			s := asStr()
			const example = "2011-01-23T05:23:12"
			if len(s) < len(example) || s[4] != '-' && s[10] != 'T' {
				panic("doesn't look like a time: " + s)
			}
			buf.WriteString(reverseTimeString(s))
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
	typeStr
	typeIntStr // integer as string
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

	// keyDeletes indexes a claim that deletes an entity. It ties the deleter
	// claim to the deleted entity.
	keyDeletes = &keyType{
		"deletes",
		[]part{
			{"deleter", typeBlobRef}, // the deleter claim blobref
			{"deleted", typeBlobRef}, // the deleted entity (a permanode or another claim)
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

	// Audio attributes (e.g., ID3 tags). Uses generic terms like
	// "artist", "title", "album", etc.
	keyAudioTag = &keyType{
		"audiotag",
		[]part{
			{"tag", typeStr},
			{"value", typeStr},
			{"wholeRef", typeBlobRef}, // wholeRef for song
		},
		nil,
	}
)
