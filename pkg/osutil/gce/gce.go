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
package gce // import "camlistore.org/pkg/osutil/gce"

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"strings"

	"camlistore.org/pkg/env"
	"camlistore.org/pkg/osutil"

	"cloud.google.com/go/compute/metadata"
	"cloud.google.com/go/logging"
	"go4.org/jsonconfig"
	"go4.org/types"
	_ "go4.org/wkfs/gcs"
	"golang.org/x/net/context"
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
	jsonconfig.RegisterFunc("_gce_instance_meta", func(c *jsonconfig.ConfigParser, v []interface{}) (interface{}, error) {
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
		for _, x := range scopes {
			if x == scope {
				return true
			}
		}
		return false
	}
	if !haveScope(logging.WriteScope) {
		return nil, fmt.Errorf("when this Google Compute Engine VM instance was created, it wasn't granted enough access to use Google Cloud Logging (Scope URL: %v).", logging.WriteScope)
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
		logger:   logc.Logger("camlistored-stderr"),
	}
	return multiWriteCloser{
		w:      io.MultiWriter(w, logw),
		closer: logc,
	}, nil
}
