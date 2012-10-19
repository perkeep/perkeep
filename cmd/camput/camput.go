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
	"net/http"
	"os"

	"camlistore.org/pkg/client"
	"camlistore.org/pkg/jsonsign"
)

const buffered = 16 // arbitrary

var (
	flagHelp    = flag.Bool("help", false, "print usage")
	flagVerbose = flag.Bool("verbose", false, "extra debug logging")
)

var ErrUsage = UsageError("invalid command usage")

type UsageError string

func (ue UsageError) Error() string {
	return "Usage error: " + string(ue)
}

type CommandRunner interface {
	Usage()
	RunCommand(up *Uploader, args []string) error
}

type Exampler interface {
	Examples() []string
}

var modeCommand = make(map[string]CommandRunner)
var modeFlags = make(map[string]*flag.FlagSet)

func RegisterCommand(mode string, makeCmd func(Flags *flag.FlagSet) CommandRunner) {
	if _, dup := modeCommand[mode]; dup {
		log.Fatalf("duplicate command %q registered", mode)
	}
	flags := flag.NewFlagSet(mode+" options", flag.ContinueOnError)
	flags.Usage = func() {}
	modeFlags[mode] = flags
	modeCommand[mode] = makeCmd(flags)
}

// wereErrors gets set to true if any error was encountered, which
// changes the os.Exit value
var wereErrors = false

type namedMode struct {
	Name    string
	Command CommandRunner
}

func allModes(startModes []string) <-chan namedMode {
	ch := make(chan namedMode)
	go func() {
		defer close(ch)
		done := map[string]bool{}
		for _, name := range startModes {
			done[name] = true
			cmd := modeCommand[name]
			if cmd == nil {
				panic("bogus mode: " + name)
			}
			ch <- namedMode{name, cmd}
		}
		for name, cmd := range modeCommand {
			if !done[name] {
				ch <- namedMode{name, cmd}
			}
		}
	}()
	return ch
}

func errf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format, args...)
}

func usage(msg string) {
	if msg != "" {
		errf("Error: %v\n", msg)
	}
	errf(`
Usage: camput [globalopts] <mode> [commandopts] [commandargs]

Examples:
`)
	order := []string{"init", "file", "permanode", "blob", "attr"}
	for mode := range allModes(order) {
		errf("\n")
		if ex, ok := mode.Command.(Exampler); ok {
			for _, example := range ex.Examples() {
				errf("  camput %s %s\n", mode.Name, example)
			}
		} else {
			errf("  camput %s ...\n", mode.Name)
		}
	}

	errf(`
For mode-specific help:

  camput <mode> -help

Global options:
`)
	flag.PrintDefaults()
	os.Exit(1)
}

func handleResult(what string, pr *client.PutResult, err error) error {
	if err != nil {
		log.Printf("Error putting %s: %s", what, err)
		wereErrors = true
		return err
	}
	fmt.Println(pr.BlobRef.String())
	return nil
}

func newUploader() *Uploader {
	cc := client.NewOrFail()
	if !*flagVerbose {
		cc.SetLogger(nil)
	}

	transport := new(tinkerTransport)
	transport.transport = &http.Transport{DisableKeepAlives: false}
	cc.SetHttpClient(&http.Client{Transport: transport})

	pwd, err := os.Getwd()
	if err != nil {
		log.Fatalf("os.Getwd: %v", err)
	}

	return &Uploader{
		Client:    cc,
		transport: transport,
		pwd:       pwd,
		entityFetcher: &jsonsign.CachingEntityFetcher{
			Fetcher: &jsonsign.FileEntityFetcher{File: cc.SecretRingFile()},
		},
	}
}

func hasFlags(flags *flag.FlagSet) bool {
	any := false
	flags.VisitAll(func(*flag.Flag) {
		any = true
	})
	return any
}

func main() {
	jsonsign.AddFlags()
	client.AddFlags()
	flag.Parse()

	if *flagHelp {
		usage("")
	}
	if flag.NArg() == 0 {
		usage("No mode given.")
	}

	mode := flag.Arg(0)
	cmd, ok := modeCommand[mode]
	if !ok {
		usage(fmt.Sprintf("Unknown mode %q", mode))
	}

	var up *Uploader
	if mode != "init" {
		up = newUploader()
	}

	cmdFlags := modeFlags[mode]
	err := cmdFlags.Parse(flag.Args()[1:])
	if err != nil {
		err = ErrUsage
	} else {
		err = cmd.RunCommand(up, cmdFlags.Args())
	}
	if ue, isUsage := err.(UsageError); isUsage {
		if isUsage {
			errf("%s\n", ue)
		}
		cmd.Usage()
		errf("\nGlobal options:\n")
		flag.PrintDefaults()

		if hasFlags(cmdFlags) {
			errf("\nMode-specific options for mode %q:\n", mode)
			cmdFlags.PrintDefaults()
		}
		os.Exit(1)
	}
	if *flagVerbose {
		stats := up.Stats()
		log.Printf("Client stats: %s", stats.String())
		log.Printf("  #HTTP reqs: %d", up.transport.reqs)
	}
	if err != nil || wereErrors /* TODO: remove this part */ {
		log.Printf("Error: %v", err)
		os.Exit(2)
	}
}
