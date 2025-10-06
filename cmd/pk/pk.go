/*
Copyright 2013 The Perkeep Authors.

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
	"log"

	"perkeep.org/pkg/client"
	"perkeep.org/pkg/cmdmain"
)

func init() {
	// So we can simply use log.Printf and log.Fatalf.
	// For logging that depends on verbosity (cmdmain.FlagVerbose), use cmdmain.Logf/Printf.
	log.SetOutput(cmdmain.Stderr)
}

var ctxbg = context.Background()

func main() {
	cmdmain.Main()
}

const serverFlagHelp = "Format is is either a URL prefix (with optional path), a host[:port], a config file server alias, or blank to use the Perkeep client config's default server."

// newClient returns a Perkeep client for the server.
// The server may be:
//   - blank, to use the default in the config file
//   - an alias, to use that named alias in the config file
//   - host:port
//   - https?://host[:port][/path]
func newClient(server string, opts ...client.ClientOption) *client.Client {
	if server == "" {
		return client.NewOrFail(opts...)
	}
	cl := client.NewOrFail(append(opts[:len(opts):cap(opts)], client.OptionServer(server))...)
	if err := cl.SetupAuth(); err != nil {
		log.Fatalf("Could not setup auth for connecting to %v: %v", server, err)
	}
	return cl
}
