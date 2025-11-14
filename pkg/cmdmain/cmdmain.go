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

// Package cmdmain contains the shared implementation for pk-get,
// pk-put, pk, and other Perkeep command-line tools.
package cmdmain // import "perkeep.org/pkg/cmdmain"

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"perkeep.org/pkg/buildinfo"

	"go4.org/legal"
)

var (
	FlagVersion = flag.Bool("version", false, "show version")
	FlagHelp    = flag.Bool("help", false, "print usage")
	FlagVerbose = flag.Bool("verbose", false, "extra debug logging")
	FlagLegal   = flag.Bool("legal", false, "show licenses")
)

var (
	// ExtraFlagRegistration allows to add more flags from
	// other packages (with AddFlags) when Main starts.
	ExtraFlagRegistration = func() {}
	// PostFlag runs code that needs to happen after flags were parsed, but
	// before the subcommand is run.
	PostFlag = func() {}
	// PreExit runs after the subcommand, but before Main terminates
	// with either success or the error from the subcommand.
	PreExit = func() {}
	// ExitWithFailure determines whether the command exits
	// with a non-zero exit status.
	ExitWithFailure bool
)

var ErrUsage = UsageError("invalid command")

type UsageError string

func (ue UsageError) Error() string {
	return "Usage error: " + string(ue)
}

var (
	// mode name to actual subcommand mapping
	modeCommand = make(map[string]CommandRunner)
	modeFlags   = make(map[string]*flag.FlagSet)
	wantHelp    = make(map[string]*bool)
	// asNewCommand stores whether the mode should actually be run as a new
	// independent command.
	asNewCommand = make(map[string]bool)

	// Indirections for replacement by tests
	Stderr io.Writer = os.Stderr
	Stdout io.Writer = os.Stdout
	Stdin  io.Reader = os.Stdin

	Exit = realExit
	// TODO: abstract out vfs operation. should never call os.Stat, os.Open, os.Create, etc.
	// Only use fs.Stat, fs.Open, where vs is an interface type.
	// TODO: switch from using the global flag FlagSet and use our own. right now
	// running "go test -v" dumps the flag usage data to the global stderr.

	logger = log.New(Stderr, "", log.LstdFlags)
)

func realExit(code int) {
	os.Exit(code)
}

// CommandRunner is the type that a command mode should implement.
type CommandRunner interface {
	Usage()
	RunCommand(args []string) error
}

// ExecRunner is the type that a command mode should implement when that mode
// just calls a new executable that will run as a new command.
type ExecRunner interface {
	CommandRunner
	LookPath() (string, error)
}

// Demoter is an interface that boring commands can implement to
// demote themselves in the tool listing, for boring or low-level
// subcommands. They only show up in --help mode.
type Demoter interface {
	CommandRunner
	Demote() bool
}

type exampler interface {
	Examples() []string
}

type describer interface {
	Describe() string
}

func demote(c CommandRunner) bool {
	i, ok := c.(Demoter)
	return ok && i.Demote()
}

// RegisterMode adds a mode to the list of modes for the main command.
// It is meant to be called in init() for each subcommand.
func RegisterMode(mode string, makeCmd func(Flags *flag.FlagSet) CommandRunner) {
	if _, dup := modeCommand[mode]; dup {
		log.Fatalf("duplicate command %q registered", mode)
	}
	flags := flag.NewFlagSet(mode+" options", flag.ContinueOnError)
	flags.Usage = func() {}

	var cmdHelp bool
	flags.BoolVar(&cmdHelp, "help", false, "Help for this mode.")
	wantHelp[mode] = &cmdHelp
	modeFlags[mode] = flags
	modeCommand[mode] = makeCmd(flags)
}

// RegisterCommand adds a mode to the list of modes for the main command, and
// also specifies that this mode is just another executable that runs as a new
// cmdmain command. The executable to run is determined by the LookPath implementation
// for this mode.
func RegisterCommand(mode string, makeCmd func(Flags *flag.FlagSet) CommandRunner) {
	RegisterMode(mode, makeCmd)
	asNewCommand[mode] = true
}

func hasFlags(flags *flag.FlagSet) bool {
	any := false
	flags.VisitAll(func(*flag.Flag) {
		any = true
	})
	return any
}

func usage(msg string) {
	cmdName := filepath.Base(os.Args[0])
	if msg != "" {
		Errorf("Error: %v\n", msg)
	}
	var modesQualifer string
	if !*FlagHelp {
		modesQualifer = " (use --help to see all modes)"
	}
	Errorf(`
Usage: `+cmdName+` [globalopts] <mode> [commandopts] [commandargs]

Modes:%s

`, modesQualifer)
	var modes []string
	for mode, cmd := range modeCommand {
		if des, ok := cmd.(describer); ok && (*FlagHelp || !demote(cmd)) {
			modes = append(modes, fmt.Sprintf("  %s: %s\n", mode, des.Describe()))
		}
	}
	sort.Strings(modes)
	for i := range modes {
		Errorf("%s", modes[i])
	}

	Errorf("\nExamples:\n")
	modes = nil
	for mode, cmd := range modeCommand {
		if ex, ok := cmd.(exampler); ok && (*FlagHelp || !demote(cmd)) {
			line := ""
			exs := ex.Examples()
			if len(exs) > 0 {
				line = "\n"
			}
			for _, example := range exs {
				line += fmt.Sprintf("  %s %s %s\n", cmdName, mode, example)
			}
			modes = append(modes, line)
		}
	}
	sort.Strings(modes)
	for i := range modes {
		Errorf("%s", modes[i])
	}

	Errorf("\nFor mode-specific help:\n\n  ")
	Errorf("%s <mode> -help\n", cmdName)

	Errorf("\nGlobal options:\n")
	flag.PrintDefaults()
	Exit(1)
}

func help(mode string) {
	cmdName := os.Args[0]
	// We can skip all the checks as they're done in Main
	cmd := modeCommand[mode]
	cmdFlags := modeFlags[mode]
	cmdFlags.SetOutput(Stderr)
	if des, ok := cmd.(describer); ok {
		Errorf("%s\n", des.Describe())
	}
	Errorf("\n")
	cmd.Usage()
	if hasFlags(cmdFlags) {
		cmdFlags.PrintDefaults()
	}
	if ex, ok := cmd.(exampler); ok {
		Errorf("\nExamples:\n")
		for _, example := range ex.Examples() {
			Errorf("  %s %s %s\n", cmdName, mode, example)
		}
	}
}

// registerFlagOnce guards ExtraFlagRegistration. Tests may invoke
// Main multiple times, but duplicate flag registration is fatal.
var registerFlagOnce sync.Once

var setCommandLineOutput func(io.Writer) // or nil if before Go 1.2

// PrintLicenses prints all the licences registered by go4.org/legal for this program.
func PrintLicenses() {
	for _, text := range legal.Licenses() {
		fmt.Fprintln(Stderr, text)
	}
}

// Main is meant to be the core of a command that has
// subcommands (modes), such as pk-put or pk.
func Main() {
	registerFlagOnce.Do(ExtraFlagRegistration)
	if setCommandLineOutput != nil {
		setCommandLineOutput(Stderr)
	}
	flag.Usage = func() {
		usage("")
	}
	flag.Parse()
	flag.CommandLine.SetOutput(Stderr)
	PostFlag()

	args := flag.Args()
	if *FlagVersion {
		fmt.Fprintf(Stderr, "%s version: %s\n", os.Args[0], buildinfo.Summary())
		return
	}
	if *FlagHelp {
		usage("")
	}
	if *FlagLegal {
		PrintLicenses()
		return
	}
	if len(args) == 0 {
		usage("No mode given.")
	}

	mode := args[0]
	cmd, ok := modeCommand[mode]
	if !ok {
		usage(fmt.Sprintf("Unknown mode %q", mode))
	}

	if _, ok := asNewCommand[mode]; ok {
		runAsNewCommand(cmd, mode)
		return
	}

	cmdFlags := modeFlags[mode]
	cmdFlags.SetOutput(Stderr)
	err := cmdFlags.Parse(args[1:])
	if err != nil {
		// We want -h to behave as -help, but without having to define another flag for
		// it, so we handle it here.
		// TODO(mpl): maybe even remove -help and just let them both be handled here?
		if err == flag.ErrHelp {
			help(mode)
			return
		}
		err = ErrUsage
	} else {
		if *wantHelp[mode] {
			help(mode)
			return
		}
		err = cmd.RunCommand(cmdFlags.Args())
	}
	if ue, isUsage := err.(UsageError); isUsage {
		if isUsage {
			Errorf("%s\n", ue)
		}
		cmd.Usage()
		Errorf("\nGlobal options:\n")
		flag.PrintDefaults()

		if hasFlags(cmdFlags) {
			Errorf("\nMode-specific options for mode %q:\n", mode)
			cmdFlags.PrintDefaults()
		}
		Exit(1)
	}
	PreExit()
	if err != nil {
		if !ExitWithFailure {
			// because it was already logged if ExitWithFailure
			Errorf("Error: %v\n", err)
		}
		Exit(2)
	}
}

// runAsNewCommand runs the executable specified by cmd's LookPath, which means
// cmd must implement the ExecRunner interface. The executable must be a binary of
// a program that runs Main.
func runAsNewCommand(cmd CommandRunner, mode string) {
	execCmd, ok := cmd.(ExecRunner)
	if !ok {
		panic(fmt.Sprintf("%v does not implement ExecRunner", mode))
	}
	cmdPath, err := execCmd.LookPath()
	if err != nil {
		Errorf("Error: %v\n", err)
		Exit(2)
	}
	allArgs := shiftFlags(mode)
	if err := runExec(cmdPath, allArgs, newCopyEnv()); err != nil {
		panic(fmt.Sprintf("running %v should have ended with an os.Exit, and not leave us with that error: %v", cmdPath, err))
	}
}

// shiftFlags prepends all the arguments (global flags) passed before the given
// mode to the list of arguments after that mode, and returns that list.
func shiftFlags(mode string) []string {
	modePos := 0
	for k, v := range os.Args {
		if v == mode {
			modePos = k
			break
		}
	}
	globalFlags := os.Args[1:modePos]
	return append(globalFlags, os.Args[modePos+1:]...)
}

// Errorf prints to Stderr, regardless of FlagVerbose.
func Errorf(format string, args ...any) {
	fmt.Fprintf(Stderr, format, args...)
}

// Printf prints to Stderr if FlagVerbose, and is silent otherwise.
func Printf(format string, args ...any) {
	if *FlagVerbose {
		fmt.Fprintf(Stderr, format, args...)
	}
}

// Logf logs to Stderr if FlagVerbose, and is silent otherwise.
func Logf(format string, v ...any) {
	if !*FlagVerbose {
		return
	}
	logger.Printf(format, v...)
}

// sysExec is set to syscall.Exec on platforms that support it.
var sysExec func(argv0 string, argv []string, envv []string) (err error)

// runExec execs bin. If the platform doesn't support exec, it runs it and waits
// for it to finish.
func runExec(bin string, args []string, e *env) error {
	if sysExec != nil {
		return sysExec(bin, append([]string{filepath.Base(bin)}, args...), e.flat())
	}

	cmd := exec.Command(bin, args...)
	cmd.Env = e.flat()
	cmd.Stdout = Stdout
	cmd.Stderr = Stderr
	return cmd.Run()
}

type env struct {
	m     map[string]string
	order []string
}

func (e *env) set(k, v string) {
	_, dup := e.m[k]
	e.m[k] = v
	if !dup {
		e.order = append(e.order, k)
	}
}

func (e *env) flat() []string {
	vv := make([]string, 0, len(e.order))
	for _, k := range e.order {
		if v, ok := e.m[k]; ok {
			vv = append(vv, k+"="+v)
		}
	}
	return vv
}

func newCopyEnv() *env {
	e := &env{make(map[string]string), nil}
	for _, kv := range os.Environ() {
		eq := strings.Index(kv, "=")
		if eq > 0 {
			e.set(kv[:eq], kv[eq+1:])
		}
	}
	return e
}
