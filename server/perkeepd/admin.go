/*
Copyright 2026 The Perkeep Authors

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
	"io"
)

func perkeepdUsage() {
	w := flag.CommandLine.Output()
	fmt.Fprint(w, `Usage:
  perkeepd [options]
  perkeepd admin <command> [options] [arguments]

Server options:
`)
	flag.PrintDefaults()
	fmt.Fprint(w, "\nRun \"perkeepd admin -help\" for administrative commands.\n")
}

// runAdmin handles administrative commands without starting the daemon.
// Exit statuses are 0 for success/help, 1 for an operation error, and 2 for
// invalid arguments. Each command has its own flags, separate from server flags.
func runAdmin(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("perkeepd admin", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprint(stderr, `Usage: perkeepd admin <command> [options] [arguments]

Commands:
  copy-storage  Copy blobs between storage defined by server configs.
  dumpconfig    Dump the low-level server config from its simple config.
  googinit      Init Google Drive or Google Cloud Storage.

Run "perkeepd admin <command> -help" for command-specific help.
`)
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	args = flags.Args()
	if len(args) == 0 {
		flags.Usage()
		return 2
	}
	if args[0] == "help" {
		if len(args) == 1 {
			flags.Usage()
			return 0
		}
		if len(args) != 2 {
			fmt.Fprintln(stderr, "perkeepd admin help accepts at most one command")
			flags.Usage()
			return 2
		}
		args = []string{args[1], "-help"}
	}
	switch args[0] {
	case "copy-storage":
		return runAdminCopyStorage(args[1:], stdout, stderr)
	case "dumpconfig":
		return runAdminDumpConfig(args[1:], stdout, stderr)
	case "googinit":
		return runAdminGoogInit(args[1:], stdin, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "perkeepd admin: unknown command %q\n", args[0])
		flags.Usage()
		return 2
	}
}
