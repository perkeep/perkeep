/*
Copyright 2013 Google Inc.

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

package schema

import (
	"encoding/json"
	"strings"

	"camlistore.org/pkg/blobref"
)

// AnyBlob represents any type of schema blob.
type AnyBlob interface {
	Blob() *Blob
}

// Buildable returns a Builder from a base.
type Buildable interface {
	Builder() *Builder
}

// A Blob represents a Camlistore schema blob.
// It is immutable.
type Blob struct {
	br  *blobref.BlobRef
	str string

	ss *Superset
}

// Type returns the blob's "camliType" field.
func (b *Blob) Type() string { return b.ss.Type }

// BlobRef returns the schema blob's blobref.
func (b *Blob) BlobRef() *blobref.BlobRef { return b.br }

// JSON returns the JSON bytes of the schema blob.
func (b *Blob) JSON() string { return b.str }

func (b *Blob) Blob() *Blob { return b }

func (b *Blob) Builder() *Builder {
	return b.jsonMap().Builder()
}

func (b *Blob) jsonMap() Map {
	var m map[string]interface{}
	err := json.Unmarshal([]byte(b.str), &m)
	if err != nil {
		panic("failed to decode previously-thought-valid Blob's JSON: " + err.Error())
	}
	return m
}

// MapToBlob is a transitional function to ease the migration from schema.Map to *schema.Blob.
// TODO(bradfitz): delete.
func MapToBlob(m Map) *Blob {
	json, err := m.JSON()
	if err != nil {
		panic(err)
	}
	ss, err := ParseSuperset(strings.NewReader(json))
	if err != nil {
		panic(err)
	}
	h := blobref.NewHash()
	h.Write([]byte(json))
	return &Blob{
		str: json,
		ss:  ss,
		br:  blobref.FromHash(h),
	}
}

// A Claim is a Blob that is signed.
type Claim struct {
	b *Blob
}

// Blob returns the claim's Blob.
func (c Claim) Blob() *Blob { return c.b }

// A Builder builds a JSON blob.
// After mutating the Builder, call Blob to get the built blob.
type Builder struct {
	m map[string]interface{}
}

// Blob builds the Blob. The builder continues to be usable after a call to Build.
func (b *Builder) Blob() *Blob {
	return MapToBlob(b.m)
}

func (b *Builder) SetSigner(signer *blobref.BlobRef) *Builder {
	b.m["camliSigner"] = signer.String()
	return b
}
