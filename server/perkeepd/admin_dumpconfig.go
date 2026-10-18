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
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"regexp"

	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/serverinit"
)

func runAdminDumpConfig(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("perkeepd admin dumpconfig", flag.ContinueOnError)
	flags.SetOutput(stderr)
	file := flags.String("configfile", "", "server config file (default: server-config.json in the Perkeep configuration directory)")
	flags.Usage = func() {
		fmt.Fprint(stderr, `Usage: perkeepd admin dumpconfig [options] [configfile]

Dump an existing high- or low-level server configuration as low-level JSON.
Relative file paths are relative to the working directory.
The output contains configuration secrets; it is not redacted.

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
	configFlag := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "configfile" {
			configFlag = true
		}
	})
	if flags.NArg() > 1 {
		fmt.Fprintln(stderr, "dumpconfig accepts at most one configuration file")
		flags.Usage()
		return 2
	}
	if flags.NArg() == 1 {
		if configFlag {
			fmt.Fprintln(stderr, "use either -configfile or a positional file, not both")
			flags.Usage()
			return 2
		}
		*file = flags.Arg(0)
	}
	if flags.NArg() == 0 && *file == "" {
		*file = osutil.UserServerConfigPath()
	}
	if err := dumpConfig(*file, stdout); err != nil {
		fmt.Fprintf(stderr, "perkeepd admin dumpconfig: %v\n", err)
		return 1
	}
	return 0
}

func dumpConfig(file string, dst io.Writer) error {
	cfg, err := serverinit.LoadFile(file)
	if err != nil {
		return err
	}
	lowj, err := json.MarshalIndent(cfg.LowLevelJSONConfig(), "", "  ") //lint:ignore SA1019 we use it still
	if err != nil {
		return err
	}
	knownKeys := regexp.MustCompile(`(?ms)^\s+"_knownkeys": {.+?},?\n`)
	lowj = knownKeys.ReplaceAll(lowj, nil)
	_, err = dst.Write(lowj)
	return err
}
