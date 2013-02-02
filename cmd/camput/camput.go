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
	"io"
	"log"
	"net/http"
	"os"
	"sort"

	"camlistore.org/pkg/buildinfo"
	"camlistore.org/pkg/client"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/jsonsign"
)

const buffered = 16 // arbitrary

var (
	flagVersion = flag.Bool("version", false, "show version")
	flagHelp    = flag.Bool("help", false, "print usage")
	flagVerbose = flag.Bool("verbose", false, "extra debug logging")
	flagHTTP    = flag.Bool("verbose_http", false, "show HTTP request summaries")
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
		var rest []string
		for name := range modeCommand {
			if !done[name] {
				rest = append(rest, name)
			}
		}
		sort.Strings(rest)
		for _, name := range rest {
			ch <- namedMode{name, modeCommand[name]}
		}
	}()
	return ch
}

func errf(format string, args ...interface{}) {
	fmt.Fprintf(stderr, format, args...)
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
	exit(1)
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

	var transport http.RoundTripper

	transport = &http.Transport{
		Dial: dialFunc(),
		TLSClientConfig: tlsClientConfig(),
	}

	httpStats := &httputil.StatsTransport{
		VerboseLog: *flagHTTP,
		Transport:  transport,
	}
	transport = httpStats

	if androidOutput {
		transport = androidStatsTransport{transport}
	}
	cc.SetHTTPClient(&http.Client{Transport: transport})

	pwd, err := os.Getwd()
	if err != nil {
		log.Fatalf("os.Getwd: %v", err)
	}

	return &Uploader{
		Client:    cc,
		transport: httpStats,
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
	camputMain(flag.Args()...)
}

func realExit(code int) {
	os.Exit(code)
}

// Indirections for replacement by tests:
var (
	stderr io.Writer = os.Stderr
	stdout io.Writer = os.Stdout
	stdin  io.Reader = os.Stdin

	exit = realExit

	// TODO: abstract out vfs operation. should never call os.Stat, os.Open, os.Create, etc.
	// Only use fs.Stat, fs.Open, where vs is an interface type.

	// TODO: switch from using the global flag FlagSet and use our own. right now
	// running "go test -v" dumps the flag usage data to the global stderr.
)

// camputMain is separated from main for testing from camput
func camputMain(args ...string) {
	if *flagVersion {
		fmt.Fprintf(stderr, "camget version: %s\n", buildinfo.Version())
		return
	}
	if *flagHelp {
		usage("")
	}
	if len(args) == 0 {
		usage("No mode given.")
	}

	mode := args[0]
	cmd, ok := modeCommand[mode]
	if !ok {
		usage(fmt.Sprintf("Unknown mode %q", mode))
	}

	var up *Uploader
	if mode != "init" {
		up = newUploader()
	}

	cmdFlags := modeFlags[mode]
	err := cmdFlags.Parse(args[1:])
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
		exit(1)
	}
	if *flagVerbose {
		stats := up.Stats()
		log.Printf("Client stats: %s", stats.String())
		log.Printf("  #HTTP reqs: %d", up.transport.Requests())
	}
	previousErrors := wereErrors
	if err != nil {
		wereErrors = true
		if !previousErrors {
			log.Printf("Error: %v", err)
		}
	}
	if wereErrors {
		exit(2)
	}
}
