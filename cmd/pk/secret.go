/*
Copyright 2026 The Perkeep Authors.

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
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"unicode/utf8"

	"perkeep.org/pkg/client"
	"perkeep.org/pkg/cmdmain"
	"perkeep.org/pkg/types/camtypes"
)

type secretCmd struct {
	server   string
	file     string
	json     bool
	rotated  bool
	revision int64
}

func (c *secretCmd) flags(f *flag.FlagSet) {
	f.StringVar(&c.server, "server", c.server, "Server to connect to. "+serverFlagHelp)
	f.StringVar(&c.file, "file", c.file, "Read set's UTF-8 value from this file; - or omitted reads stdin.")
	f.BoolVar(&c.json, "json", c.json, "Print JSON, including migration metadata (get also includes the value).")
	f.BoolVar(&c.rotated, "rotated", c.rotated, "Confirm set's new value was rotated; clear the legacy migration warning.")
	f.Int64Var(&c.revision, "revision", c.revision, "Expected revision for set/delete; -1 is unconditional, 0 is create-only.")
}

func init() {
	cmdmain.RegisterMode("secret", func(f *flag.FlagSet) cmdmain.CommandRunner {
		c := &secretCmd{revision: -1}
		c.flags(f)
		return c
	})
}

func (*secretCmd) Describe() string {
	return "List, read, set, or delete owner-only server secrets (not blobs)."
}
func (*secretCmd) Usage() {
	fmt.Fprintln(cmdmain.Stderr, `Usage: pk secret [options] list
       pk secret [options] get NAME
       pk secret [options] set NAME < value-file
       pk secret [options] set --file VALUE-FILE NAME
       pk secret [options] delete NAME

Set reads exact UTF-8 text from stdin or --file, never a command-line value.
Use --revision for compare-and-swap. After rotating a migrated credential,
use set --rotated to explicitly clear its warning. Options may also follow
list/get/set/delete, before NAME. Get writes the exact value to stdout;
legacy migration warnings always go to stderr, even when not a terminal.`)
}

func (c *secretCmd) parse(args []string) (verb, name string, err error) {
	if len(args) == 0 {
		return "", "", cmdmain.UsageError("secret requires list, get, set, or delete")
	}
	verb = args[0]
	switch verb {
	case "list", "get", "set", "delete":
	default:
		return "", "", cmdmain.UsageError("unknown secret subcommand")
	}
	f := flag.NewFlagSet("secret "+verb, flag.ContinueOnError)
	f.SetOutput(io.Discard) // Flag errors must not echo command-line input.
	c.flags(f)
	if err := f.Parse(args[1:]); err != nil {
		return "", "", cmdmain.UsageError("invalid secret options")
	}
	rest := f.Args()
	if verb == "list" {
		if len(rest) != 0 {
			return "", "", cmdmain.UsageError("secret list takes no names")
		}
	} else {
		if len(rest) != 1 || rest[0] == "" {
			return "", "", cmdmain.UsageError("secret operation requires exactly one name; set reads its value from stdin or --file")
		}
		name = rest[0]
	}
	if c.revision < -1 {
		return "", "", cmdmain.UsageError("revision must be -1, 0, or a positive revision")
	}
	if verb != "set" && (c.file != "" || c.rotated) {
		return "", "", cmdmain.UsageError("--file and --rotated require set")
	}
	return verb, name, nil
}

func (c *secretCmd) RunCommand(args []string) error {
	verb, name, err := c.parse(args)
	if err != nil {
		return err
	}
	// Keep the ordinary configured owner credentials, not discovery's UI token.
	var opts []client.ClientOption
	if c.server != "" {
		opts = append(opts, client.OptionServer(c.server))
	}
	cl, err := client.New(opts...)
	if err != nil {
		return err
	}
	defer cl.Close()
	if c.server != "" {
		if err := cl.SetupAuth(); err != nil {
			return err
		}
	}
	return c.run(ctxbg, cl, verb, name, cmdmain.Stdin, cmdmain.Stdout, cmdmain.Stderr)
}

type secretClient interface {
	ListSecrets(context.Context) ([]camtypes.SecretMetadata, error)
	GetSecret(context.Context, string) (camtypes.SecretEntry, error)
	PutSecret(context.Context, string, string, client.SecretPutOptions) (camtypes.SecretMetadata, error)
	DeleteSecret(context.Context, string, int64) error
}

const secretMigrationWarning = "⚠ This secret was migrated from a permanode and may remain in immutable blobs. Rotate the credential, then use pk secret set --rotated to clear this warning."

func (c *secretCmd) run(ctx context.Context, cl secretClient, verb, name string, stdin io.Reader, stdout, stderr io.Writer) error {
	switch verb {
	case "list":
		entries, err := cl.ListSecrets(ctx)
		if err != nil {
			return err
		}
		if c.json {
			return json.NewEncoder(stdout).Encode(entries)
		}
		for _, m := range entries {
			marker := ""
			if m.MigratedFromPermanode {
				marker = "\t⚠ migratedFromPermanode:true; rotate credential"
			}
			if _, err := fmt.Fprintf(stdout, "%s\t%d\t%s%s\n", m.Name, m.Revision, m.Updated.Format("2006-01-02T15:04:05Z07:00"), marker); err != nil {
				return err
			}
		}
		return nil
	case "get":
		entry, err := cl.GetSecret(ctx, name)
		if err != nil {
			return err
		}
		if entry.MigratedFromPermanode {
			fmt.Fprintln(stderr, secretMigrationWarning)
		}
		if c.json {
			return json.NewEncoder(stdout).Encode(entry)
		}
		_, err = io.WriteString(stdout, entry.Value)
		return err
	case "set":
		src := stdin
		if c.file != "" && c.file != "-" {
			f, err := os.Open(c.file)
			if err != nil {
				return errors.New("could not open secret input file")
			}
			defer f.Close()
			src = f
		}
		const maxSecretValue = 1 << 20
		b, err := io.ReadAll(io.LimitReader(src, maxSecretValue+1))
		if err != nil {
			return errors.New("could not read secret input")
		}
		if len(b) > maxSecretValue {
			return errors.New("secret value exceeds 1 MiB")
		}
		if !utf8.Valid(b) {
			return errors.New("secret value must be valid UTF-8")
		}
		meta, err := cl.PutSecret(ctx, name, string(b), client.SecretPutOptions{ExpectedRevision: c.revision, ClearMigration: c.rotated})
		if err != nil {
			return err
		}
		if meta.MigratedFromPermanode {
			fmt.Fprintln(stderr, secretMigrationWarning)
		}
		if c.json {
			return json.NewEncoder(stdout).Encode(meta)
		}
		_, err = fmt.Fprintf(stdout, "%s\trevision %d\n", meta.Name, meta.Revision)
		return err
	case "delete":
		return cl.DeleteSecret(ctx, name, c.revision)
	}
	return errors.New("unknown secret operation")
}
