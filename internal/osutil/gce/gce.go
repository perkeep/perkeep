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

// Package gce configures hooks for running Perkeep for Google Compute Engine.
package gce // import "perkeep.org/internal/osutil/gce"

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2/google"
	compute "google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/env"

	"cloud.google.com/go/compute/metadata"
	"cloud.google.com/go/logging"
	"go4.org/jsonconfig"
	"go4.org/types"
	_ "go4.org/wkfs/gcs"
)

func init() {
	if !env.OnGCE() {
		return
	}
	osutil.RegisterConfigDirFunc(func() string {
		v, _ := metadata.InstanceAttributeValue("camlistore-config-dir")
		if v == "" {
			return v
		}
		return path.Clean("/gcs/" + strings.TrimPrefix(v, "gs://"))
	})
	jsonconfig.RegisterFunc("_gce_instance_meta", func(c *jsonconfig.ConfigParser, v []any) (any, error) {
		if len(v) != 1 {
			return nil, errors.New("only 1 argument supported after _gce_instance_meta")
		}
		attr, ok := v[0].(string)
		if !ok {
			return nil, errors.New("expected argument after _gce_instance_meta to be a string")
		}
		val, err := metadata.InstanceAttributeValue(attr)
		if err != nil {
			return nil, fmt.Errorf("error reading GCE instance attribute %q: %v", attr, err)
		}
		return val, nil
	})
}

type writer struct {
	severity logging.Severity
	logger   *logging.Logger
}

func (w writer) Write(p []byte) (n int, err error) {
	w.logger.Log(logging.Entry{
		Severity: w.severity,
		Payload:  string(p),
	})
	return len(p), nil
}

type multiWriteCloser struct {
	w      io.Writer
	closer io.Closer
}

func (mwc multiWriteCloser) Write(p []byte) (n int, err error) {
	return mwc.w.Write(p)
}

func (mwc multiWriteCloser) Close() error {
	return mwc.closer.Close()
}

// LogWriter returns an environment-specific io.WriteCloser suitable for passing
// to log.SetOutput. It will also include writing to os.Stderr as well.
// Since it might be writing to a Google Cloud Logger, it is the responsibility
// of the caller to Close it when needed, to flush the last log entries.
func LogWriter() (w io.WriteCloser, err error) {
	w = multiWriteCloser{
		w: os.Stderr,
		// Because we don't actually want to close os.Stderr (which we could).
		closer: types.NopCloser,
	}
	if !env.OnGCE() {
		return
	}
	projID, err := metadata.ProjectID()
	if projID == "" {
		log.Printf("Error getting project ID: %v", err)
		return
	}
	scopes, _ := metadata.Scopes("default")
	haveScope := func(scope string) bool {
		return slices.Contains(scopes, scope)
	}
	if !haveScope(logging.WriteScope) {
		return nil, fmt.Errorf("when this Google Compute Engine VM instance was created, it wasn't granted enough access to use Google Cloud Logging (Scope URL: %v)", logging.WriteScope)
	}

	ctx := context.Background()
	logc, err := logging.NewClient(ctx, projID)
	if err != nil {
		return nil, fmt.Errorf("error creating Google logging client: %v", err)
	}
	if err := logc.Ping(ctx); err != nil {
		return nil, fmt.Errorf("Google logging client not ready (ping failed): %v", err)
	}
	logw := writer{
		severity: logging.Debug,
		logger:   logc.Logger("perkeepd-stderr"),
	}
	return multiWriteCloser{
		w:      io.MultiWriter(w, logw),
		closer: logc,
	}, nil
}

type gceInst struct {
	cs        *compute.Service
	cis       *compute.InstancesService
	zone      string
	projectID string
	name      string
}

func gceInstance() (*gceInst, error) {
	ctx := context.Background()
	hc, err := google.DefaultClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting a default http client: %v", err)
	}
	cs, err := compute.NewService(ctx, option.WithHTTPClient(hc))
	if err != nil {
		return nil, fmt.Errorf("error getting a compute service: %v", err)
	}
	cis := compute.NewInstancesService(cs)
	projectID, err := metadata.ProjectID()
	if err != nil {
		return nil, fmt.Errorf("error getting projectID: %v", err)
	}
	zone, err := metadata.Zone()
	if err != nil {
		return nil, fmt.Errorf("error getting zone: %v", err)
	}
	name, err := metadata.InstanceName()
	if err != nil {
		return nil, fmt.Errorf("error getting instance name: %v", err)
	}
	return &gceInst{
		cs:        cs,
		cis:       cis,
		zone:      zone,
		projectID: projectID,
		name:      name,
	}, nil
}

// resetInstance reboots the GCE VM that this process is running in.
func resetInstance() error {
	if !env.OnGCE() {
		return errors.New("cannot reset instance if not on GCE")
	}

	ctx := context.Background()

	inst, err := gceInstance()
	if err != nil {
		return err
	}
	cs, projectID, zone, name := inst.cis, inst.projectID, inst.zone, inst.name

	call := cs.Reset(projectID, zone, name).Context(ctx)
	op, err := call.Do()
	if err != nil {
		if googleapi.IsNotModified(err) {
			return nil
		}
		return fmt.Errorf("error resetting instance: %v", err)
	}
	// TODO(mpl): refactor this whole pattern below into a func
	opName := op.Name
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
		op, err := inst.cs.ZoneOperations.Get(projectID, zone, opName).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("failed to get op %s: %v", opName, err)
		}
		switch op.Status {
		case "PENDING", "RUNNING":
			continue
		case "DONE":
			if op.Error != nil {
				for _, operr := range op.Error.Errors {
					log.Printf("operation error: %+v", operr)
				}
				return fmt.Errorf("operation error: %v", op.Error.Errors[0])
			}
			log.Print("Successfully reset instance")
			return nil
		default:
			return fmt.Errorf("unknown operation status %q: %+v", op.Status, op)
		}
	}
}

// SetInstanceHostname sets the "camlistore-hostname" metadata on the GCE
// instance where perkeepd is running. The value set is the same as the one we
// register with the camlistore.net DNS, i.e. "<gpgKeyId>.camlistore.net", where
// <gpgKeyId> is the short form (8 trailing chars) of Perkeep's keyId.
func SetInstanceHostname(camliNetHostName string) error {
	if !env.OnGCE() {
		return nil
	}

	hostname, err := metadata.InstanceAttributeValue("camlistore-hostname")
	if err != nil {
		if _, ok := err.(metadata.NotDefinedError); !ok {
			return fmt.Errorf("error getting existing camlistore-hostname: %v", err)
		}
	}
	if err == nil && hostname != "" {
		// we do not overwrite an existing value. it's not possible anyway, as the
		// SetMetadata call won't allow it.
		return nil
	}

	ctx := context.Background()
	inst, err := gceInstance()
	if err != nil {
		return err
	}
	cs, projectID, zone, name := inst.cis, inst.projectID, inst.zone, inst.name

	instance, err := cs.Get(projectID, zone, name).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("error getting instance: %v", err)
	}
	items := instance.Metadata.Items
	items = append(items, &compute.MetadataItems{
		Key:   "camlistore-hostname",
		Value: googleapi.String(camliNetHostName),
	})
	mdata := &compute.Metadata{
		Items:       items,
		Fingerprint: instance.Metadata.Fingerprint,
	}

	call := cs.SetMetadata(projectID, zone, name, mdata).Context(ctx)
	op, err := call.Do()
	if err != nil {
		if googleapi.IsNotModified(err) {
			return nil
		}
		return fmt.Errorf("error setting instance hostname: %v", err)
	}
	// TODO(mpl): refactor this whole pattern below into a func
	opName := op.Name
	for {
		// TODO(mpl): add a timeout maybe?
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
		op, err := inst.cs.ZoneOperations.Get(projectID, zone, opName).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("failed to get op %s: %v", opName, err)
		}
		switch op.Status {
		case "PENDING", "RUNNING":
			continue
		case "DONE":
			if op.Error != nil {
				for _, operr := range op.Error.Errors {
					log.Printf("operation error: %+v", operr)
				}
				return fmt.Errorf("operation error: %v", op.Error.Errors[0])
			}
			log.Printf(`Successfully set "camlistore-hostname" to "%v" on instance`, camliNetHostName)
			return nil
		default:
			return fmt.Errorf("unknown operation status %q: %+v", op.Status, op)
		}
	}
}

func exitf(pattern string, args ...any) {
	log.SetOutput(os.Stderr)
	log.SetFlags(0)
	log.Fatalf(pattern, args...)
}

// FixUserDataForPerkeepRename checks whether the value of "user-data"
// in the GCE metadata is up to date with the correct systemd service
// and docker image tarball based on the "perkeep" name. If not
// (i.e. they're the old "camlistore" based ones), it fixes said
// metadata. It returns whether the metadata was indeed changed, which
// indicates that the instance should be restarted for the change to
// take effect.
func FixUserDataForPerkeepRename() {
	needsRestart, err := fixUserDataForPerkeepRename()
	if err != nil {
		exitf("Could not fix GCE user-data metadata: %v", err)
	}
	if needsRestart {
		if err := resetInstance(); err != nil {
			exitf("Could not reset instance: %v", err)
		}
	}
}

func fixUserDataForPerkeepRename() (needsRestart bool, err error) {
	if !env.OnGCE() {
		return false, nil
	}

	metadataKey := "user-data"

	userData, err := metadata.InstanceAttributeValue(metadataKey)
	if err != nil {
		if _, ok := err.(metadata.NotDefinedError); !ok {
			return false, fmt.Errorf("error getting existing user-data: %v", err)
		}
	}

	goodExecStartPre := `ExecStartPre=/bin/bash -c '/usr/bin/curl https://storage.googleapis.com/camlistore-release/docker/perkeepd.tar.gz`
	goodExecStart := `ExecStart=/opt/bin/systemd-docker run --rm -p 80:80 -p 443:443 --name %n -v /run/camjournald.sock:/run/camjournald.sock -v /var/lib/camlistore/tmp:/tmp --link=mysql.service:mysqldb perkeep/server`
	goodServiceName := `- name: perkeepd.service`
	if strings.Contains(userData, goodExecStartPre) &&
		strings.Contains(userData, goodExecStart) &&
		strings.Contains(userData, goodServiceName) {
		// We're already a proper perkeep deployment, all good.
		return false, nil
	}

	oldExecStartPre := `ExecStartPre=/bin/bash -c '/usr/bin/curl https://storage.googleapis.com/camlistore-release/docker/camlistored.tar.gz`
	oldExecStart := `ExecStart=/opt/bin/systemd-docker run --rm -p 80:80 -p 443:443 --name %n -v /run/camjournald.sock:/run/camjournald.sock -v /var/lib/camlistore/tmp:/tmp --link=mysql.service:mysqldb camlistore/server`

	// double-check that it's our launcher based instance, and not a custom thing,
	// even though OnGCE is already a pretty strong barrier.
	if !strings.Contains(userData, oldExecStartPre) {
		return false, nil
	}

	oldServiceName := `- name: camlistored.service`
	userData = strings.Replace(userData, oldExecStartPre, goodExecStartPre, 1)
	userData = strings.Replace(userData, oldExecStart, goodExecStart, 1)
	userData = strings.Replace(userData, oldServiceName, goodServiceName, 1)

	ctx := context.Background()
	inst, err := gceInstance()
	if err != nil {
		return false, err
	}
	cs, projectID, zone, name := inst.cis, inst.projectID, inst.zone, inst.name

	instance, err := cs.Get(projectID, zone, name).Context(ctx).Do()
	if err != nil {
		return false, fmt.Errorf("error getting instance: %v", err)
	}
	items := instance.Metadata.Items
	for k, v := range items {
		if v.Key == metadataKey {
			items[k] = &compute.MetadataItems{
				Key:   metadataKey,
				Value: googleapi.String(userData),
			}
			break
		}
	}
	mdata := &compute.Metadata{
		Items:       items,
		Fingerprint: instance.Metadata.Fingerprint,
	}

	call := cs.SetMetadata(projectID, zone, name, mdata).Context(ctx)
	op, err := call.Do()
	if err != nil {
		if googleapi.IsNotModified(err) {
			return false, nil
		}
		return false, fmt.Errorf("error setting instance user-data: %v", err)
	}
	// TODO(mpl): refactor this whole pattern below into a func
	opName := op.Name
	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
		op, err := inst.cs.ZoneOperations.Get(projectID, zone, opName).Context(ctx).Do()
		if err != nil {
			return false, fmt.Errorf("failed to get op %s: %v", opName, err)
		}
		switch op.Status {
		case "PENDING", "RUNNING":
			continue
		case "DONE":
			if op.Error != nil {
				for _, operr := range op.Error.Errors {
					log.Printf("operation error: %+v", operr)
				}
				return false, fmt.Errorf("operation error: %v", op.Error.Errors[0])
			}
			log.Printf("Successfully corrected %v on instance", metadataKey)
			return true, nil
		default:
			return false, fmt.Errorf("unknown operation status %q: %+v", op.Status, op)
		}
	}
}

// BlobpackedRecoveryValue returns the blobpacked recovery level (0, 1, 2) from
// the GCE instance metadata.
//
// The return type here is logically a blobpacked.RecoveryMode, but we're not
// importing that here to save a dependency.
func BlobpackedRecoveryValue() int {
	recovery, err := metadata.InstanceAttributeValue("camlistore-recovery")
	if err != nil {
		if _, ok := err.(metadata.NotDefinedError); !ok {
			log.Printf("error getting camlistore-recovery: %v", err)
		}
		return 0
	}
	if recovery == "" {
		return 0
	}
	mode, err := strconv.Atoi(recovery)
	if err != nil {
		log.Printf("invalid int value for \"camlistore-recovery\": %v", err)
	}
	return mode
}
