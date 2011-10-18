/*
Copyright 2011 Google Inc.

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
	"flag"
	"fmt"
	"log"
	"runtime"
	"strings"
	"os"
	"path/filepath"

	"camli/osutil"
	"camli/serverconfig"
	"camli/webserver"

	// Storage options:
	_ "camli/blobserver/cond"
	_ "camli/blobserver/localdisk"
	_ "camli/blobserver/remote"
	_ "camli/blobserver/replica"
	_ "camli/blobserver/s3"
	_ "camli/blobserver/shard"
	_ "camli/mysqlindexer" // indexer, but uses storage interface
	// Handlers:
	_ "camli/search"
	_ "camli/server" // UI, publish, etc
)

var flagConfigFile = flag.String("configfile", "serverconfig",
	"Config file to use, relative to camli config dir root, or blank to not use config files.")

func exitFailure(pattern string, args ...interface{}) {
	if !strings.HasSuffix(pattern, "\n") {
		pattern = pattern + "\n"
	}
	fmt.Fprintf(os.Stderr, pattern, args...)
	os.Exit(1)
}

func main() {
	flag.Parse()

	file := *flagConfigFile
	if !filepath.IsAbs(file) {
		file = filepath.Join(osutil.CamliConfigDir(), file)
	}
	config, err := serverconfig.Load(file)
	if err != nil {
		exitFailure("Could not load server config: %v", err)
	}

	ws := webserver.New()
	baseURL := ws.BaseURL()

	{
		cert, key := config.OptionalString("TLSCertFile", ""), config.OptionalString("TLSKeyFile", "")
		if (cert != "") != (key != "") {
			exitFailure("TLSCertFile and TLSKeyFile must both be either present or absent")
		}
		if cert != "" {
			ws.SetTLS(cert, key)
		}
	}

	err = config.InstallHandlers(ws, baseURL)
	if err != nil {
		exitFailure("Error parsing config: %v", err)
	}

	ws.Listen()

	if config.UIPath != "" {
		uiURL := ws.BaseURL() + config.UIPath
		log.Printf("UI available at %s", uiURL)
		if runtime.GOOS == "windows" {
			// Might be double-clicking an icon with no shell window?
			// Just open the URL for them.
			osutil.OpenURL(uiURL)
		}
	}
	ws.Serve()
}
