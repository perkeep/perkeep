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

// TODO(mpl): this doc is not in sync with what the pk-put help outputs. But it should be.

/*
The pk-put tool mainly pushes blobs, files, and directories. It can also perform various related tasks, such as setting tags, creating permanodes, and creating share blobs.


Usage:

  pk-put [globalopts] <mode> [commandopts] [commandargs]

Modes:

  delete: Create and upload a delete claim.
  attr: Add, set, or delete a permanode's attribute.
  file: Upload file(s).
  init: Initialize the pk-put configuration file. With no option, it tries to use the GPG key found in the default identity secret ring.
  permanode: Create and upload a permanode.
  rawobj: Upload a custom JSON schema blob.
  share: Grant access to a resource by making a "share" blob.
  blob: Upload raw blob(s).

Examples:

  pk-put file [opts] <file(s)/director(ies)
  pk-put file --permanode --title='Homedir backup' --tag=backup,homedir $HOME
  pk-put file --filenodes /mnt/camera/DCIM

  pk-put blob <files>     (raw, without any metadata)
  pk-put blob --permanode --title='My Blob' --tag=backup,my_blob
  pk-put blob -           (read from stdin)

  pk-put permanode                                (create a new permanode)
  pk-put permanode --title="Some Name" --tag=foo,bar (with attributes added)

  pk-put init
  pk-put init --gpgkey=XXXXX

  pk-put share [opts] <blobref to share via haveref>

  pk-put rawobj (debug command)

  pk-put attr <permanode> <name> <value>         Set attribute
  pk-put attr --add <permanode> <name> <value>   Adds attribute (e.g. "tag")
  pk-put attr --del <permanode> <name> [<value>] Deletes named attribute [value

For mode-specific help:

  pk-put <mode> -help

Global options:
  -help=false: print usage
  -secret-keyring="~/.gnupg/secring.gpg": GnuPG secret keyring file to use.
  -server="": Perkeep server prefix. If blank, the default from the "server" field of
  ~/.camlistore/config is used.
  Acceptable forms: https://you.example.com, example.com:1345 (https assumed),
  or http://you.example.com/alt-root
  -verbose=false: extra debug logging
  -verbose_http=false: show HTTP request summaries
  -version=false: show version
*/
package main // import "perkeep.org/cmd/pk-put"
