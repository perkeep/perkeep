/*
Copyright 2014 The Perkeep Authors

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

	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/env"
	"perkeep.org/pkg/types/serverconfig"

	"cloud.google.com/go/compute/metadata"
)

const (
	// useDBNamesConfig is a sentinel value for DBUnique to indicate that we want the
	// low-level configuration generator to keep on using the old DBNames
	// style configuration for database names.
	useDBNamesConfig = "useDBNamesConfig"
)

// DefaultEnvConfig returns the default configuration when running on a known
// environment. Currently this just includes Google Compute Engine.
// If the environment isn't known (nil, nil) is returned.
func DefaultEnvConfig() (*Config, error) {
	if !env.OnGCE() {
		return nil, nil
	}
	auth := "none"
	user, _ := metadata.InstanceAttributeValue("camlistore-username")
	pass, _ := metadata.InstanceAttributeValue("camlistore-password")
	confBucket, err := metadata.InstanceAttributeValue("camlistore-config-dir")
	if confBucket == "" || err != nil {
		return nil, fmt.Errorf("VM instance metadata key 'camlistore-config-dir' not set: %v", err)
	}
	blobBucket, err := metadata.InstanceAttributeValue("camlistore-blob-dir")
	if blobBucket == "" || err != nil {
		return nil, fmt.Errorf("VM instance metadata key 'camlistore-blob-dir' not set: %v", err)
	}
	if user != "" && pass != "" {
		auth = "userpass:" + user + ":" + pass
	}

	if v := osutil.SecretRingFile(); !strings.HasPrefix(v, "/gcs/") {
		return nil, fmt.Errorf("Internal error: secret ring path on GCE should be at /gcs/, not %q", v)
	}
	keyID, secRing, err := getOrMakeKeyring()
	if err != nil {
		return nil, err
	}

	highConf := &serverconfig.Config{
		Auth:               auth,
		HTTPS:              true,
		Identity:           keyID,
		IdentitySecretRing: secRing,
		GoogleCloudStorage: ":" + strings.TrimPrefix(blobBucket, "gs://"),
		PackRelated:        true,
		ShareHandler:       true,
	}

	hostName, _ := metadata.InstanceAttributeValue("camlistore-hostname")
	// If they specified a hostname (previously common with old pk-deploy), then:
	// if it looks like an FQDN, perkeepd is going to rely on Let's
	// Encrypt, else perkeepd is going to generate some self-signed for that
	// hostname.
	// Also, if the hostname is in camlistore.net, we want Perkeep to initialize
	// exactly as if the instance had no hostname, so that it registers its hostname/IP
	// with the camlistore.net DNS server (possibly needlessly, if the instance IP has
	// not changed) again.
	if hostName != "" && !strings.HasSuffix(hostName, "camlistore.net") {
		highConf.BaseURL = fmt.Sprintf("https://%s", hostName)
		highConf.Listen = "0.0.0.0:443"
	} else {
		panic("unsupported legacy configuration using camlistore.net is no longer supported")
	}

	// Detect a linked Docker MySQL container. It must have alias "mysqldb".
	mysqlPort := os.Getenv("MYSQLDB_PORT")
	if !strings.HasPrefix(mysqlPort, "tcp://") {
		// No MySQL
		// TODO: also detect Cloud SQL.
		highConf.KVFile = "/index.kv"
		return genLowLevelConfig(highConf)
	}
	hostPort := strings.TrimPrefix(mysqlPort, "tcp://")
	highConf.MySQL = "root@" + hostPort + ":" // no password
	configVersion, err := metadata.InstanceAttributeValue("perkeep-config-version")
	if configVersion == "" || err != nil {
		// the launcher is deploying a pre-"perkeep-config-version" Perkeep, which means
		// we want the old configuration, with DBNames
		highConf.DBUnique = useDBNamesConfig
	} else if configVersion != "1" {
		return nil, fmt.Errorf("unexpected value for VM instance metadata key 'perkeep-config-version': %q", configVersion)
	}

	conf, err := genLowLevelConfig(highConf)
	if err != nil {
		return nil, err
	}

	if err := conf.readFields(); err != nil {
		return nil, err
	}
	return conf, nil
}
