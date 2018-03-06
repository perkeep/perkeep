/*
Copyright 2018 The Perkeep Authors.

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
	"os/exec"
	"path/filepath"

	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/cmdmain"
)

type putCmd struct {
	binName string // the executable that is actually called, i.e. "pk-put".
}

func init() {
	cmdmain.RegisterCommand("put", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		return &putCmd{
			binName: "pk-put",
		}
		// TODO(mpl): do cmdmain.ExtraFlagRegistration = client.AddFlags somewhere, so
		// pk has same global flags as pk-put? (which I think it already does anyway).
	})
}

func (c *putCmd) Describe() string {
	return "Create and upload blobs to a server."
}

func (c *putCmd) Usage() {
	panic("pk put Usage should never get called, as we should always end up calling either pk's or pk-put's usage")
}

func (c *putCmd) RunCommand(args []string) error {
	// RunCommand is only implemented to satisfy the CommandRunner interface.
	panic("pk put RunCommand should never get called, as pk is supposed to invoke pk-put instead.")
}

// LookPath returns the full path to the executable that "pk put" actually
// calls, i.e. "pk-put".
func (c *putCmd) LookPath() (string, error) {
	fullPath, err := exec.LookPath(c.binName)
	if err == nil {
		return fullPath, nil
	}
	// If not in PATH, also try in bin dir of Perkeep source tree
	perkeepPath, err := osutil.GoPackagePath("perkeep.org")
	if err != nil {
		return "", fmt.Errorf("full path for %v command was not found in either PATH or the bin dir of the source tree", c.binName)
	}
	fullPath = filepath.Join(perkeepPath, "bin", c.binName)
	if _, err := os.Stat(fullPath); err != nil {
		return "", fmt.Errorf("full path for %v command was not found in either PATH or %v", c.binName, filepath.Join(perkeepPath, "bin"))
	}
	return fullPath, nil
}
