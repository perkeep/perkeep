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

package mysql

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"golang.org/x/net/context"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	sqladmin "google.golang.org/api/sqladmin/v1beta3"
	"google.golang.org/cloud/compute/metadata"
)

const cloudSQLSuffix = ".cloudsql.google.internal"

func maybeRemapCloudSQL(host string) (out string, err error) {
	if !strings.HasSuffix(host, cloudSQLSuffix) {
		return host, nil
	}
	inst := strings.TrimSuffix(host, cloudSQLSuffix)
	if !metadata.OnGCE() {
		return "", errors.New("CloudSQL support only available when running on Google Compute Engine.")
	}
	proj, err := metadata.ProjectID()
	if err != nil {
		return "", fmt.Errorf("Failed to lookup GCE project ID: %v", err)
	}

	admin, _ := sqladmin.New(oauth2.NewClient(context.Background(), google.ComputeTokenSource("")))
	listRes, err := admin.Instances.List(proj).Do()
	if err != nil {
		return "", fmt.Errorf("error enumerating Cloud SQL instances: %v", err)
	}
	for _, it := range listRes.Items {
		if !strings.EqualFold(it.Instance, inst) {
			continue
		}
		js, _ := json.Marshal(it)
		log.Printf("Found Cloud SQL instance %s: %s", inst, js)
		for _, ipm := range it.IpAddresses {
			return ipm.IpAddress, nil
		}
		return "", fmt.Errorf("No external IP address for Cloud SQL instances %s", inst)
	}
	var found []string
	for _, it := range listRes.Items {
		found = append(found, it.Instance)
	}
	return "", fmt.Errorf("Cloud SQL instance %q not found. Found: %q", inst, found)
}
