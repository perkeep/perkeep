/*
Copyright 2013 Google Inc.

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
	"errors"
	"flag"
	"fmt"
	"log"
	"os"

	"camlistore.org/pkg/blobserver/diskpacked"
	"camlistore.org/pkg/cmdmain"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/osutil"
	"camlistore.org/pkg/serverconfig"
)

type reindexdpCmd struct {
	overwrite, verbose bool
}

func init() {
	cmdmain.RegisterCommand("reindex-diskpacked",
		func(flags *flag.FlagSet) cmdmain.CommandRunner {
			cmd := new(reindexdpCmd)
			flags.BoolVar(&cmd.overwrite, "overwrite", false,
				"Overwrite the existing index.kv? If not, than only checking is made.")
			return cmd
		})
}

func (c *reindexdpCmd) Describe() string {
	return "Rebuild the index of the diskpacked blob store"
}

func (c *reindexdpCmd) Usage() {
	fmt.Fprintln(os.Stderr, "Usage: camtool [globalopts] reindex-diskpacked [reindex-opts]")
	fmt.Fprintln(os.Stderr, "       camtool reindex-diskpacked")
	fmt.Fprintln(os.Stderr, "       camtool reindex-diskpacked --overwrite")
}

func (c *reindexdpCmd) RunCommand(args []string) error {
	var path string
	switch {
	case len(args) == 0:
		cfg, err := serverconfig.Load(osutil.UserServerConfigPath())
		if err != nil {
			return err
		}
		prefixes, ok := cfg.Obj["prefixes"].(map[string]interface{})
		if !ok {
			return fmt.Errorf("No 'prefixes' object in low-level (or converted) config file %s", osutil.UserServerConfigPath())
		}
		paths := []string{}
		for prefix, vei := range prefixes {
			pmap, ok := vei.(map[string]interface{})
			if !ok {
				log.Printf("prefix %q value is a %T, not an object", prefix, vei)
				continue
			}
			pconf := jsonconfig.Obj(pmap)
			handlerType := pconf.RequiredString("handler")
			handlerArgs := pconf.OptionalObject("handlerArgs")
			// no pconf.Validate, as this is a recover tool
			if handlerType != "storage-diskpacked" {
				continue
			}
			if handlerArgs == nil {
				log.Printf("no handlerArgs for %q", prefix)
				continue
			}
			aconf := jsonconfig.Obj(handlerArgs)
			path = aconf.RequiredString("path")
			// no aconv.Validate, as this is a recover tool
			if path != "" {
				paths = append(paths, path)
			}
		}
		if len(paths) == 0 {
			return fmt.Errorf("Server config file %s doesn't specify a disk-packed storage handler.",
				osutil.UserServerConfigPath())
		}
		if len(paths) > 1 {
			return fmt.Errorf("Ambiguity. Server config file %s d specify more than 1 disk-packed storage handler. Please specify one of: %v", osutil.UserServerConfigPath(), paths)
		}
		path = paths[0]
	case len(args) == 1:
		path = args[0]
	default:
		return errors.New("More than 1 argument not allowed")
	}
	if path == "" {
		return errors.New("no path is given/found")
	}

	return diskpacked.Reindex(path, c.overwrite)
}
