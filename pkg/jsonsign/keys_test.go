/*
Copyright 2021 The Perkeep Authors

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

package jsonsign

import (
	"strings"
	"testing"

	"golang.org/x/crypto/openpgp"
)

func TestNewEntity(t *testing.T) {
	// Create a new entity.
	entity, err := NewEntity()
	if err != nil {
		t.Fatal(err)
	}

	// Ensure that it contains at least one identity.
	if !(len(entity.Identities) > 0) {
		t.Fatal("No identities found, keys like this cannot be imported in GNUPG")
	}

	// Armor it.
	armored, err := ArmoredPublicKey(entity)
	if err != nil {
		t.Fatalf("Couldn't armor the entity: %s", err)
	}

	// Load it again.
	// This used to fail with: `entity without any identities`
	// Keys without identities are also incompatible with GNUPG.
	// See: https://github.com/perkeep/perkeep/issues/1562
	entityList, err := openpgp.ReadArmoredKeyRing(strings.NewReader(armored))
	if err != nil {
		t.Fatalf("Couldn't load the armored key: %s", err)
	}

	// Ensure that it contains one entity
	if expected, got := 1, len(entityList); expected != got {
		t.Fatalf("Expected %d entities, got %d", expected, got)
	}

	// Ensure that the entity contains one identity
	if expected, got := 1, len(entityList[0].Identities); expected != got {
		t.Fatalf("Expected %d identities, got %d", expected, got)
	}
}
