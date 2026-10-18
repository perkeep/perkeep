/*
Copyright 2013 The Perkeep Authors

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
	"errors"
	"flag"
	"fmt"
	"io"
	"log"

	"go4.org/jsonconfig"
	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/blobserver/diskpacked"
	"perkeep.org/pkg/env"
	"perkeep.org/pkg/serverinit"
)

func runAdminReindexDiskpacked(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("perkeepd admin reindex-diskpacked", flag.ContinueOnError)
	flags.SetOutput(stderr)
	overwrite := flags.Bool("overwrite", false, "Whether to overwrite the existing index. If false, only check.")
	flags.Usage = func() {
		fmt.Fprint(stderr, `Usage: perkeepd admin reindex-diskpacked [--overwrite] [path]

Check or rebuild the index of a diskpacked blob store while the server is stopped.
The default server configuration selects the store; path, if given, must match
one of its diskpacked handlers. No other handlers are opened.
The existing index backend is opened even when only checking.

Options:
`)
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() > 1 {
		fmt.Fprintln(stderr, "reindex-diskpacked accepts at most one path")
		flags.Usage()
		return 2
	}
	if err := reindexDiskpacked(flags.Arg(0), *overwrite, log.New(stderr, "", log.LstdFlags)); err != nil {
		fmt.Fprintf(stderr, "perkeepd admin reindex-diskpacked: %v\n", err)
		return 1
	}
	return 0
}

func reindexDiskpacked(path string, overwrite bool, logger *log.Logger) error {
	configFile := osutil.UserServerConfigPath()
	var indexConf jsonconfig.Obj
	cfg, err := serverinit.LoadFile(configFile)
	if err != nil {
		return err
	}
	low := cfg.LowLevelJSONConfig() //lint:ignore SA1019 we use it
	prefixes, ok := low["prefixes"].(map[string]any)
	if !ok {
		return fmt.Errorf("No 'prefixes' object in low-level (or converted) config file %s", configFile)
	}
	paths, confs := []string{}, []jsonconfig.Obj{}
	for prefix, vei := range prefixes {
		pmap, ok := vei.(map[string]any)
		if !ok {
			logger.Printf("prefix %q value is a %T, not an object", prefix, vei)
			continue
		}
		pconf := jsonconfig.Obj(pmap)
		handlerType := pconf.RequiredString("handler")
		handlerArgs := pconf.OptionalObject("handlerArgs")
		// no pconf.Validate, as this is a recover tool
		if handlerType != "storage-diskpacked" {
			continue
		}
		logger.Printf("handlerArgs of %q: %v", prefix, handlerArgs)
		if handlerArgs == nil {
			logger.Printf("no handlerArgs for %q", prefix)
			continue
		}
		aconf := jsonconfig.Obj(handlerArgs)
		apath := aconf.RequiredString("path")
		// no aconv.Validate, as this is a recover tool
		if apath == "" {
			logger.Printf("path is missing for %q", prefix)
			continue
		}
		if path != "" && path != apath {
			continue
		}
		paths = append(paths, apath)
		confs = append(confs, aconf)
	}
	if len(paths) == 0 {
		return fmt.Errorf("server config file %s doesn't specify a disk-packed storage handler",
			configFile)
	}
	if len(paths) > 1 {
		return fmt.Errorf("Ambiguity. Server config file %s d specify more than 1 disk-packed storage handler. Please specify one of: %v", configFile, paths)
	}
	path = paths[0]
	if path == "" {
		return errors.New("no path is given/found")
	}
	// If no index is specified, the default will be used (as on the regular path).
	if mi := confs[0]["metaIndex"]; mi != nil {
		if mi, ok := mi.(map[string]any); ok {
			indexConf = jsonconfig.Obj(mi)
		}
	}
	logger.Printf("indexConf: %v", indexConf)

	ctx := diskpacked.CtxSetVerbose(context.Background(), env.IsDebug())
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	return diskpacked.Reindex(ctx, path, overwrite, indexConf)
}
