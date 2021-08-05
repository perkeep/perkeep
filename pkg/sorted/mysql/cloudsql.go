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

package mysql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"cloud.google.com/go/compute/metadata"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
	sqladmin "google.golang.org/api/sqladmin/v1beta4"
)

const cloudSQLSuffix = ".cloudsql.google.internal"

func maybeRemapCloudSQL(host string) (out string, err error) {
	if !strings.HasSuffix(host, cloudSQLSuffix) {
		return host, nil
	}
	inst := strings.TrimSuffix(host, cloudSQLSuffix)
	if !metadata.OnGCE() {
		return "", errors.New("cloudSQL support only available when running on Google Compute Engine")
	}
	proj, err := metadata.ProjectID()
	if err != nil {
		return "", fmt.Errorf("failed to lookup GCE project ID: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	admin, _ := sqladmin.NewService(ctx, option.WithHTTPClient(oauth2.NewClient(context.Background(), google.ComputeTokenSource(""))))
	cancel()
	listRes, err := admin.Instances.List(proj).Do()
	if err != nil {
		return "", fmt.Errorf("error enumerating Cloud SQL instances: %v", err)
	}
	for _, it := range listRes.Items {
		if !strings.EqualFold(it.Name, inst) {
			continue
		}
		js, _ := json.Marshal(it)
		log.Printf("Found Cloud SQL instance %s: %s", inst, js)
		for _, ipm := range it.IpAddresses {
			return ipm.IpAddress, nil
		}
		return "", fmt.Errorf("no external IP address for Cloud SQL instances %s", inst)
	}
	found := make([]string, 0, len(listRes.Items))
	for _, it := range listRes.Items {
		found = append(found, it.Name)
	}
	return "", fmt.Errorf("cloud SQL instance %q not found, only: %q", inst, found)
}
