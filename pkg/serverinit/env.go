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

package serverinit

import (
	"errors"
	"fmt"
	"strings"

	"camlistore.org/pkg/osutil"
	"camlistore.org/pkg/types/serverconfig"
	"camlistore.org/third_party/github.com/bradfitz/gce"
)

// DefaultEnvConfig returns the default configuration when running on a known
// environment. Currently this just includes Google Compute Engine.
// If the environment isn't known (nil, nil) is returned.
func DefaultEnvConfig() (*Config, error) {
	if !gce.OnGCE() {
		return nil, nil
	}
	auth := "none"
	user, _ := gce.InstanceAttributeValue("camlistore-username")
	pass, _ := gce.InstanceAttributeValue("camlistore-password")
	confBucket, _ := gce.InstanceAttributeValue("camlistore-config-bucket")
	blobBucket, _ := gce.InstanceAttributeValue("camlistore-blob-bucket")
	if confBucket == "" {
		return nil, errors.New("VM instance metadata key 'camlistore-config-bucket' not set.")
	}
	if blobBucket == "" {
		return nil, errors.New("VM instance metadata key 'camlistore-blob-bucket' not set.")
	}
	if user != "" && pass != "" {
		auth = "userpass:" + user + ":" + pass
	}

	if v := osutil.SecretRingFile(); !strings.HasPrefix(v, "/gcs/") {
		return nil, fmt.Errorf("Internal error: secret ring path on GCE should be at /gcs/, not %q", v)
	}
	keyId, secRing, err := getOrMakeKeyring()
	if err != nil {
		return nil, err
	}

	return genLowLevelConfig(&serverconfig.Config{
		Auth:               auth,
		HTTPS:              true,
		Listen:             "0.0.0.0:443",
		Identity:           keyId,
		IdentitySecretRing: secRing,
		GoogleCloudStorage: ":" + strings.TrimSuffix(strings.TrimPrefix(blobBucket, "gs://"), "/"),
		KVFile:             "/index.kv", // TODO: switch to Cloud SQL or local MySQL
	})
}
