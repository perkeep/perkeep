// +build !windows

/*
Copyright 2017 The Camlistore Authors.

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
	"io"
	"log"
	"log/syslog"
)

func init() {
	setupLoggingSyslog = func() io.Closer {
		if !*flagSyslog {
			return nil
		}
		slog, err := syslog.New(syslog.LOG_INFO|syslog.LOG_DAEMON, "camlistored")
		if err != nil {
			exitf("Error connecting to syslog: %v", err)
		}
		log.SetOutput(slog)
		return slog
	}
}
