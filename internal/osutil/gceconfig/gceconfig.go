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

// Package gceconfig registers the GCE config-directory hook and
// _gce_instance_meta JSON configuration macro for Perkeep GCE instances.
package gceconfig // import "perkeep.org/internal/osutil/gceconfig"

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"cloud.google.com/go/compute/metadata"
	"go4.org/jsonconfig"
	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/env"
)

func init() {
	registerHooks(env.OnGCE, metadata.InstanceAttributeValue, osutil.RegisterConfigDirFunc, jsonconfig.RegisterFunc)
}

// registerHooks keeps metadata lookups and global registrations injectable for
// tests. The gate is env.OnGCE, not metadata.OnGCE: these hooks require the
// camlistore-config-dir instance attribute, not merely a GCE metadata server.
func registerHooks(
	onGCE func() bool,
	instanceAttributeValue func(string) (string, error),
	registerConfigDir func(func() string),
	registerJSONFunc func(string, func(*jsonconfig.ConfigParser, []any) (any, error)),
) {
	if !onGCE() {
		return
	}
	registerConfigDir(func() string {
		v, _ := instanceAttributeValue("camlistore-config-dir")
		if v == "" {
			return v
		}
		return path.Clean("/gcs/" + strings.TrimPrefix(v, "gs://"))
	})
	registerJSONFunc("_gce_instance_meta", func(c *jsonconfig.ConfigParser, v []any) (any, error) {
		if len(v) != 1 {
			return nil, errors.New("only 1 argument supported after _gce_instance_meta")
		}
		attr, ok := v[0].(string)
		if !ok {
			return nil, errors.New("expected argument after _gce_instance_meta to be a string")
		}
		val, err := instanceAttributeValue(attr)
		if err != nil {
			return nil, fmt.Errorf("error reading GCE instance attribute %q: %w", attr, err)
		}
		return val, nil
	})
}
