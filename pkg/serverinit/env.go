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
	"fmt"
	"os"
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
	confBucket, err := gce.InstanceAttributeValue("camlistore-config-bucket")
	if confBucket == "" || err != nil {
		return nil, fmt.Errorf("VM instance metadata key 'camlistore-config-bucket' not set: %v", err)
	}
	blobBucket, err := gce.InstanceAttributeValue("camlistore-blob-bucket")
	if blobBucket == "" || err != nil {
		return nil, fmt.Errorf("VM instance metadata key 'camlistore-blob-bucket' not set: %v", err)
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

	ipOrHost, _ := gce.ExternalIP()
	host, _ := gce.InstanceAttributeValue("camlistore-hostname")
	if host != "" {
		ipOrHost = host
	}

	highConf := &serverconfig.Config{
		Auth:               auth,
		BaseURL:            fmt.Sprintf("https://%s", ipOrHost),
		HTTPS:              true,
		Listen:             "0.0.0.0:443",
		Identity:           keyId,
		IdentitySecretRing: secRing,
		GoogleCloudStorage: ":" + strings.TrimSuffix(strings.TrimPrefix(blobBucket, "gs://"), "/"),
		DBNames:            map[string]string{},
	}

	// Detect a linked Docker MySQL container. It must have alias "mysqldb".
	if v := os.Getenv("MYSQLDB_PORT"); strings.HasPrefix(v, "tcp://") {
		hostPort := strings.TrimPrefix(v, "tcp://")
		highConf.MySQL = "root@" + hostPort + ":" // no password
		highConf.DBNames["queue-sync-to-index"] = "sync_index_queue"
		highConf.DBNames["ui_thumbcache"] = "ui_thumbmeta_cache"
	} else {
		// TODO: also detect Cloud SQL.
		highConf.KVFile = "/index.kv"
	}

	return genLowLevelConfig(highConf)
}
