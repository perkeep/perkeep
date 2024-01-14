/*
Copyright 2018 The Perkeep Authors

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

package main

import (
	"context"
	"flag"
	"io"
	"log"
	"os"
	"runtime"
	"strconv"

	"cloud.google.com/go/logging"
	"go4.org/types"
	"google.golang.org/api/option"
	"perkeep.org/internal/osutil/gce"
	"perkeep.org/pkg/env"
)

// For logging on Google Cloud Logging when not running on Google Compute Engine
// (for debugging).
var (
	flagGCEProjectID string
	flagGCELogName   string
	flagGCEJWTFile   string
)

func init() {
	if debug, _ := strconv.ParseBool(os.Getenv("CAMLI_MORE_FLAGS")); debug {
		flag.StringVar(&flagGCEProjectID, "gce_project_id", "", "GCE project ID; required by --gce_log_name.")
		flag.StringVar(&flagGCELogName, "gce_log_name", "", "log all messages to that log name on Google Cloud Logging as well.")
		flag.StringVar(&flagGCEJWTFile, "gce_jwt_file", "", "Filename to the GCE Service Account's JWT (JSON) config file; required by --gce_log_name.")
	}
}

// TODO(mpl): maybe export gce.writer, and reuse it here. Later.

// gclWriter is an io.Writer, where each Write writes a log entry to Google
// Cloud Logging.
type gclWriter struct {
	severity logging.Severity
	logger   *logging.Logger
}

func (w gclWriter) Write(p []byte) (n int, err error) {
	w.logger.Log(logging.Entry{
		Severity: w.severity,
		Payload:  string(p),
	})
	return len(p), nil
}

// if a non-nil logging Client is returned, it should be closed before the
// program terminates to flush any buffered log entries.
func maybeSetupGoogleCloudLogging() io.Closer {
	if flagGCEProjectID == "" && flagGCELogName == "" && flagGCEJWTFile == "" {
		return types.NopCloser
	}
	if flagGCEProjectID == "" || flagGCELogName == "" || flagGCEJWTFile == "" {
		exitf("All of --gce_project_id, --gce_log_name, and --gce_jwt_file must be specified for logging on Google Cloud Logging.")
	}
	ctx := context.Background()
	logc, err := logging.NewClient(ctx,
		flagGCEProjectID, option.WithCredentialsFile(flagGCEJWTFile))
	if err != nil {
		exitf("Error creating GCL client: %v", err)
	}
	if err := logc.Ping(ctx); err != nil {
		exitf("Google logging client not ready (ping failed): %v", err)
	}
	logw := gclWriter{
		severity: logging.Debug,
		logger:   logc.Logger(flagGCELogName),
	}
	log.SetOutput(io.MultiWriter(os.Stderr, logw))
	return logc
}

// setupLoggingSyslog is non-nil on Unix. If it returns a non-nil io.Closer log
// flush function, setupLogging returns that flush function.
var setupLoggingSyslog func() io.Closer

// setupLogging sets up logging and returns an io.Closer that flushes logs.
func setupLogging() io.Closer {
	if *flagSyslog && runtime.GOOS == "windows" {
		exitf("-syslog not available on windows")
	}
	if fn := setupLoggingSyslog; fn != nil {
		if flusher := fn(); flusher != nil {
			return flusher
		}
	}
	if env.OnGCE() {
		lw, err := gce.LogWriter()
		if err != nil {
			log.Fatalf("Error setting up logging: %v", err)
		}
		log.SetOutput(lw)
		return lw
	}
	return maybeSetupGoogleCloudLogging()
}
