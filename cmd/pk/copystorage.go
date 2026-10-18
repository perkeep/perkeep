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
	"flag"

	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/cmdmain"
)

type copyStorageCmd struct {
	binName string
}

func init() {
	cmdmain.RegisterCommand("copy-storage", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		return &copyStorageCmd{binName: "perkeepd"}
	})
}

func (*copyStorageCmd) Demote() bool { return true }

func (*copyStorageCmd) Describe() string {
	return "Copy blobs between storage defined by server configs, such as to convert a storage to another layout."
}

func (*copyStorageCmd) Usage() {
	panic("pk copy-storage Usage should never get called; perkeepd admin copy-storage provides its own usage")
}

func (*copyStorageCmd) RunCommand([]string) error {
	panic("pk copy-storage RunCommand should never get called; pk invokes perkeepd admin copy-storage instead")
}

func (c *copyStorageCmd) LookPath() (string, error) {
	return osutil.LookPathGopath(c.binName)
}

func (*copyStorageCmd) ExecArgs(args []string) []string {
	return append([]string{"admin", "copy-storage"}, args...)
}
