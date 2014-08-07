/*
Copyright 2014 The Camlistore Authors

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

// Package gce configures hooks for running Camlistore for Google Compute Engine.
package gce

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/osutil"
	_ "camlistore.org/pkg/wkfs/gcs"
	"camlistore.org/third_party/github.com/bradfitz/gce"
)

func init() {
	if !gce.OnGCE() {
		return
	}
	osutil.RegisterConfigDirFunc(func() string {
		v, _ := gce.InstanceAttributeValue("camlistore-config-bucket")
		if v == "" {
			return v
		}
		return path.Clean("/gcs/" + strings.TrimPrefix(v, "gs://"))
	})
	jsonconfig.RegisterFunc("_gce_instance_meta", func(c *jsonconfig.ConfigParser, v []interface{}) (interface{}, error) {
		if len(v) != 1 {
			return nil, errors.New("only 1 argument supported after _gce_instance_meta")
		}
		attr, ok := v[0].(string)
		if !ok {
			return nil, errors.New("expected argument after _gce_instance_meta to be a string")
		}
		val, err := gce.InstanceAttributeValue(attr)
		if err != nil {
			return nil, fmt.Errorf("error reading GCE instance attribute %q: %v", attr, err)
		}
		return val, nil
	})
}
