/*
Copyright 2026 The Perkeep Authors

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
	"errors"
	"fmt"
	"strings"

	"perkeep.org/pkg/blob"
)

// AttributeClaim is an attribute operation with its resolved permanode.
// For a multi claim, the enclosing claim supplies the date, signer, and signature.
type AttributeClaim struct {
	Type             ClaimType
	Permanode        blob.Ref
	Attribute, Value string
}

// NewMultiClaim groups attribute claim builders into one unsigned multi claim,
// preserving their order. Only add-, set-, and del-attribute claims are allowed.
// The operation fields are copied; the builders' dates, signers, signatures, and
// other envelope fields are not included in the children.
//
// A child with an absent or empty permanode requires a default set with
// SetMultiClaimPermanode before the resulting claim's AttributeClaims is called.
// Empty child permanodes are omitted from the resulting JSON.
// All other operation fields are validated immediately. An empty list is valid.
func NewMultiClaim(claims ...*Builder) (*Builder, error) {
	children := make([]any, 0, len(claims))
	for i, claim := range claims {
		if claim == nil || claim.Type() != TypeClaim {
			return nil, fmt.Errorf("schema: claims[%d]: expected an attribute claim builder", i)
		}
		child := attributeClaimFields(claim.m)
		if _, err := parseAttributeClaim(child, blob.Ref{}, true); err != nil {
			return nil, fmt.Errorf("schema: claims[%d]: %w", i, err)
		}
		if child["permaNode"] == "" {
			delete(child, "permaNode")
		}
		children = append(children, child)
	}
	bb := newClaim()
	bb.m["claims"] = children
	return bb, nil
}

// SetMultiClaimPermanode sets the default permanode for a multi claim's children.
// A zero ref removes the default. It panics if bb is not a multi claim.
func (bb *Builder) SetMultiClaimPermanode(permanode blob.Ref) *Builder {
	if bb.Type() != TypeClaim || bb.ClaimType() != MultiClaim {
		panic("called SetMultiClaimPermanode on non-multi")
	}
	if permanode.Valid() {
		bb.m["permaNode"] = permanode.String()
	} else {
		delete(bb.m, "permaNode")
	}
	return bb
}

// AttributeClaims returns the attribute operations in this claim. An ordinary
// attribute claim returns one operation; a multi claim returns its children in
// array order, resolving absent or empty child permanodes against the enclosing
// claim's permaNode. An empty array is a valid no-op.
//
// Every operation is validated before any are returned. On error the result is
// nil, never a partially valid list. Child claimDate, camliSigner, and camliSig
// fields are prohibited, even if empty or null. Validation only examines the
// schema; it does not fetch or require any referenced blobs to be available.
func (c Claim) AttributeClaims() ([]AttributeClaim, error) {
	if c.b == nil {
		return nil, errors.New("schema: nil claim")
	}
	// Decode the original JSON, rather than a typed superset, to distinguish
	// missing fields, null, and values of the wrong JSON type.
	var envelope map[string]any
	dec := json.NewDecoder(strings.NewReader(c.b.str))
	dec.UseNumber()
	if err := dec.Decode(&envelope); err != nil {
		return nil, fmt.Errorf("schema: invalid claim JSON: %w", err)
	}
	if c.ClaimType() != MultiClaim {
		operation, err := parseAttributeClaim(attributeClaimFields(envelope), blob.Ref{}, false)
		if err != nil {
			return nil, fmt.Errorf("schema: %w", err)
		}
		return []AttributeClaim{operation}, nil
	}

	var parent blob.Ref
	if value, present := envelope["permaNode"]; present {
		s, ok := value.(string)
		if !ok {
			return nil, errors.New("schema: permaNode must be a string")
		}
		if s != "" {
			var valid bool
			parent, valid = blob.Parse(s)
			if !valid {
				return nil, errors.New("schema: invalid permaNode")
			}
		}
	}
	children, ok := envelope["claims"].([]any)
	if !ok {
		return nil, errors.New("schema: claims must be an array")
	}
	operations := make([]AttributeClaim, 0, len(children))
	for i, value := range children {
		child, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("schema: claims[%d]: expected an object", i)
		}
		operation, err := parseAttributeClaim(child, parent, false)
		if err != nil {
			return nil, fmt.Errorf("schema: claims[%d]: %w", i, err)
		}
		operations = append(operations, operation)
	}
	return operations, nil
}

// attributeClaimFields copies only the fields describing an attribute operation.
// Validated fields are strings, so the result does not alias mutable values.
func attributeClaimFields(m map[string]any) map[string]any {
	fields := make(map[string]any)
	for _, key := range []string{"claimType", "permaNode", "attribute", "value"} {
		if value, present := m[key]; present {
			fields[key] = value
		}
	}
	return fields
}

// allowUnresolved permits builders to set a default permanode after construction.
// It never permits malformed, non-empty references.
func parseAttributeClaim(m map[string]any, parent blob.Ref, allowUnresolved bool) (AttributeClaim, error) {
	var operation AttributeClaim
	for _, key := range []string{"claimDate", "camliSigner", "camliSig"} {
		if _, present := m[key]; present {
			return operation, fmt.Errorf("%s is prohibited in a child claim", key)
		}
	}
	claimType, ok := m["claimType"].(string)
	if !ok {
		return operation, errors.New("claimType must be a string")
	}
	operation.Type = ClaimType(claimType)
	switch operation.Type {
	case AddAttributeClaim, SetAttributeClaim, DelAttributeClaim:
	default:
		return AttributeClaim{}, fmt.Errorf("unsupported attribute claimType %q", claimType)
	}
	operation.Permanode = parent
	if value, present := m["permaNode"]; present {
		s, ok := value.(string)
		if !ok {
			return AttributeClaim{}, errors.New("permaNode must be a string")
		}
		if s != "" {
			var valid bool
			operation.Permanode, valid = blob.Parse(s)
			if !valid {
				return AttributeClaim{}, errors.New("invalid permaNode")
			}
		}
	}
	if !operation.Permanode.Valid() && !allowUnresolved {
		return AttributeClaim{}, errors.New("missing permaNode")
	}
	operation.Attribute, ok = m["attribute"].(string)
	if !ok || operation.Attribute == "" {
		return AttributeClaim{}, errors.New("attribute must be a non-empty string")
	}
	value, present := m["value"]
	if present {
		operation.Value, ok = value.(string)
		if !ok {
			return AttributeClaim{}, errors.New("value must be a string")
		}
	} else if operation.Type != DelAttributeClaim {
		return AttributeClaim{}, errors.New("missing value")
	}
	return operation, nil
}
