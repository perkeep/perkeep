/*
Copyright 2018 The Perkeep Authors

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
	"crypto/rsa"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"

	"perkeep.org/pkg/client"
	"perkeep.org/pkg/cmdmain"
	"perkeep.org/pkg/schema"
)

type whoamiCmd struct{}

func init() {
	cmdmain.RegisterMode("whoami", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		return new(whoamiCmd)
	})
}

func (c *whoamiCmd) Describe() string {
	return "Show Perkeep identity information"
}

func (c *whoamiCmd) Usage() {
	fmt.Fprintf(os.Stderr, "pk whoami [key]\n")
}

var whoCmds = map[string]func(c *client.Client, s *schema.Signer) error{
	"identity": whoamiIdentity,
	"type":     whoamiType,
	"private":  whoamiPrivate,
}

func (c *whoamiCmd) RunCommand(args []string) error {
	if len(args) == 0 {
		args = []string{"identity"}
	}
	if len(args) > 1 {
		return cmdmain.UsageError("only 0 or 1 arguments allowed")
	}

	cc, err := client.New()
	if err != nil {
		return fmt.Errorf("creating Client: %v", err)
	}
	signer, err := cc.Signer()
	if err != nil {
		return fmt.Errorf("no configured Signer: %v", err)
	}

	cmd, ok := whoCmds[args[0]]
	if !ok {
		var cmds []string
		for k := range whoCmds {
			cmds = append(cmds, k)
		}
		sort.Strings(cmds)
		return fmt.Errorf("invalid whoami subcommand. Valid options: %q", cmds)
	}
	return cmd(cc, signer)
}

func whoamiIdentity(c *client.Client, s *schema.Signer) error {
	v := s.KeyIDLong()
	if v == "" {
		return errors.New("no configured identity")
	}
	fmt.Println("perkeepid:" + v)
	return nil
}

func whoamiType(c *client.Client, s *schema.Signer) error {
	ent := s.Entity()
	if ent == nil {
		return errors.New("no identity")
	}
	fmt.Printf("%T\n", ent.PrivateKey.PrivateKey)
	return nil
}

func whoamiPrivate(c *client.Client, s *schema.Signer) error {
	ent := s.Entity()
	if ent == nil {
		return errors.New("no identity")
	}
	switch v := ent.PrivateKey.PrivateKey.(type) {
	case *rsa.PrivateKey:
		fmt.Printf("public.N = %s\n", v.N.String())
		fmt.Printf("public.E = %v\n", v.E)
		fmt.Printf("private.D = %s\n", v.D.String())
		return nil
	default:
		return fmt.Errorf("unhandled private key %T", v)
	}
}
