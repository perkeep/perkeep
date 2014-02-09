/*
Copyright 2013 The Camlistore Authors.

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
	"os"
	"strconv"

	"camlistore.org/pkg/client"
	"camlistore.org/pkg/cmdmain"
)

type indexCmd struct {
	verbose     bool
	wipe        bool
	insecureTLS bool
}

func init() {
	cmdmain.RegisterCommand("index", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		cmd := new(indexCmd)
		flags.BoolVar(&cmd.verbose, "verbose", false, "Be verbose.")
		flags.BoolVar(&cmd.wipe, "wipe", false, "Erase and recreate all discovered indexes. NOOP for now.")
		if debug, _ := strconv.ParseBool(os.Getenv("CAMLI_DEBUG")); debug {
			flags.BoolVar(&cmd.insecureTLS, "insecure", false, "If set, when using TLS, the server's certificates verification is disabled, and they are not checked against the trustedCerts in the client configuration either.")
		}
		return cmd
	})
}

func (c *indexCmd) Describe() string {
	return "Synchronize blobs for all discovered blobs storage - indexer pairs."
}

func (c *indexCmd) Usage() {
	fmt.Fprintf(os.Stderr, "Usage: camtool [globalopts] index [indexopts] \n")
}

func (c *indexCmd) RunCommand(args []string) error {
	dc := c.discoClient()
	syncHandlers, err := dc.SyncHandlers()
	if err != nil {
		return fmt.Errorf("sync handlers discovery failed: %v", err)
	}

	for _, sh := range syncHandlers {
		if sh.ToIndex {
			if err := c.sync(sh.From, sh.To); err != nil {
				return fmt.Errorf("Error while indexing from %v to %v: %v", sh.From, sh.To, err)
			}
		}
	}
	return nil
}

func (c *indexCmd) sync(from, to string) error {
	return (&syncCmd{
		src:     from,
		dest:    to,
		verbose: c.verbose,
		wipe:    c.wipe,
	}).RunCommand(nil)
}

// discoClient returns a client initialized with a server
// based from the configuration file. The returned client
// can then be used to discover the blobRoot and syncHandlers.
func (c *indexCmd) discoClient() *client.Client {
	return newClient("")
}
