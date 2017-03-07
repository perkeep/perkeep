// +build !js

/*
Copyright 2017 The Camlistore Authors

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

package env

import (
	"sync"

	"cloud.google.com/go/compute/metadata"
)

// TODO(mpl): report to the gopherjs project that the
// generated javascript breaks as soon as we import things such as
// "cloud.google.com/go/compute/metadata", which is why this whole file exists
// under this build tag.
// Keeping https://github.com/camlistore/camlistore/issues/904 open until this
// is reported.

// OnGCE reports whether this process is running in a Google Compute
// Engine (GCE) environment.  This only returns true if the
// "camlistore-config-dir" instance metadata value is defined.
// Instances running in custom configs on GCE will be unaffected.
func OnGCE() bool {
	gceOnce.Do(detectGCE)
	return isGCE
}

var (
	gceOnce sync.Once
	isGCE   bool
)

func detectGCE() {
	if !metadata.OnGCE() {
		return
	}
	v, _ := metadata.InstanceAttributeValue("camlistore-config-dir")
	isGCE = v != ""
}
