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

	"camlistore.org/pkg/env"
	"camlistore.org/pkg/osutil"
	"camlistore.org/pkg/types/serverconfig"

	"cloud.google.com/go/compute/metadata"
)

// For getting a name in camlistore.net
const (
	// CamliNetDNS is the hostname of the camlistore.net DNS server.
	CamliNetDNS = "camnetdns.camlistore.org"
	// CamliNetDomain is the camlistore.net domain name. It is relevant to
	// Camlistore, because a deployment through the Camlistore on Google Cloud launcher
	// automatically offers a subdomain name in this domain to any instance.
	CamliNetDomain = "camlistore.net"
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
	keyId, secRing, err := getOrMakeKeyring()
	if err != nil {
		return nil, err
	}

	highConf := &serverconfig.Config{
		Auth:               auth,
		HTTPS:              true,
		Identity:           keyId,
		IdentitySecretRing: secRing,
		GoogleCloudStorage: ":" + strings.TrimPrefix(blobBucket, "gs://"),
		DBNames:            map[string]string{},
		PackRelated:        true,
		ShareHandler:       true,
	}

	externalIP, _ := metadata.ExternalIP()
	hostName, _ := metadata.InstanceAttributeValue("camlistore-hostname")
	// If they specified a hostname (probably with camdeploy), then:
	// if it looks like an FQDN, camlistored is going to rely on Let's
	// Encrypt, else camlistored is going to generate some self-signed for that
	// hostname.
	// Also, if the hostname is in camlistore.net, we want Camlistore to initialize
	// exactly as if the instance had no hostname, so that it registers its hostname/IP
	// with the camlistore.net DNS server (possibly needlessly, if the instance IP has
	// not changed) again.
	if hostName != "" && !strings.HasSuffix(hostName, CamliNetDomain) {
		highConf.BaseURL = fmt.Sprintf("https://%s", hostName)
		highConf.Listen = "0.0.0.0:443"
	} else {
		highConf.CamliNetIP = externalIP
	}

	// Detect a linked Docker MySQL container. It must have alias "mysqldb".
	if v := os.Getenv("MYSQLDB_PORT"); strings.HasPrefix(v, "tcp://") {
		hostPort := strings.TrimPrefix(v, "tcp://")
		highConf.MySQL = "root@" + hostPort + ":" // no password
		highConf.DBNames["queue-sync-to-index"] = "sync_index_queue"
		highConf.DBNames["ui_thumbcache"] = "ui_thumbmeta_cache"
		highConf.DBNames["blobpacked_index"] = "blobpacked_index"
	} else {
		// TODO: also detect Cloud SQL.
		highConf.KVFile = "/index.kv"
	}

	return genLowLevelConfig(highConf)
}
