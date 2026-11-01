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

package serverinit

import (
	"errors"
	"fmt"

	"go4.org/jsonconfig"
)

// ensureSecretStore upgrades older low-level configurations. Importers must
// never silently use a process-local store in a real server: startup migration
// removes their current credential attributes after copying them to this store.
// High-level configurations already name the store explicitly.
func ensureSecretStore(prefixes jsonconfig.Obj) error {
	var secretRoots []string
	var importers, roots []map[string]any
	for prefix, raw := range prefixes {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue // addPrefixes reports malformed entries.
		}
		if enabled, ok := entry["enabled"].(bool); ok && !enabled {
			continue
		}
		switch entry["handler"] {
		case "secrets":
			secretRoots = append(secretRoots, prefix)
		case "importer":
			importers = append(importers, entry)
		case "root":
			roots = append(roots, entry)
		}
	}
	if len(importers) == 0 {
		return nil
	}
	if len(secretRoots) == 0 {
		if _, exists := prefixes["/secrets/"]; exists {
			return errors.New("importers require a secret store, but /secrets/ is disabled or used by another handler")
		}
		prefixes["/secrets/"] = map[string]any{
			"handler":     "secrets",
			"handlerArgs": map[string]any{},
		}
		secretRoots = append(secretRoots, "/secrets/")
	}
	argsFor := func(entry map[string]any) (map[string]any, error) {
		if entry["handlerArgs"] == nil {
			entry["handlerArgs"] = map[string]any{}
		}
		args, ok := entry["handlerArgs"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%v handlerArgs is not an object", entry["handler"])
		}
		return args, nil
	}
	for _, entries := range [][]map[string]any{importers, roots} {
		for _, entry := range entries {
			args, err := argsFor(entry)
			if err != nil {
				return err
			}
			if value, exists := args["secretRoot"]; exists && value != "" {
				continue
			}
			if len(secretRoots) != 1 {
				return errors.New("multiple secret stores: set secretRoot explicitly on importer and root handlers")
			}
			args["secretRoot"] = secretRoots[0]
		}
	}
	return nil
}
