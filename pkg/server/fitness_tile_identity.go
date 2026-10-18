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

package server

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/buildinfo"
)

// fitnessDefaultTileUserAgent identifies the application and, for OSM only,
// distinguishes operators without sending their signing public key blobrefs.
// This is a stable pseudonym, not anonymization: a known public key can be
// checked against it. Never send it to an unrelated custom tile provider.
func fitnessDefaultTileUserAgent(template string, owner blob.Ref) string {
	version := buildinfo.Version
	if version == "" {
		version = buildinfo.GitInfo
	}
	if version == "" {
		version = "unknown"
	}
	// Git build descriptions can contain spaces; keep the product version a
	// single HTTP token even for locally supplied build information.
	version = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' ||
			r >= '0' && r <= '9' || strings.ContainsRune("-._+", r) {
			return r
		}
		return '-'
	}, version)
	ua := "Perkeep/" + version + " (+https://perkeep.org/"
	if template == fitnessTileDefaultURL && owner.Valid() {
		sum := sha256.Sum256([]byte("osm\x00" + owner.String()))
		ua += "; operator-id=" + hex.EncodeToString(sum[:16])
	}
	return ua + ")"
}
