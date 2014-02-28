/*
Copyright 2013 The Camlistore Authors.

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

/*
The camput tool mainly pushes blobs, files, and directories. It can also perform various related tasks, such as setting tags, creating permanodes, and creating share blobs.


Usage:

  camput [globalopts] <mode> [commandopts] [commandargs]

Modes:

  delete: Create and upload a delete claim.
  attr: Add, set, or delete a permanode's attribute.
  file: Upload file(s).
  init: Initialize the camput configuration file. With no option, it tries to use the GPG key found in the default identity secret ring.
  permanode: Create and upload a permanode.
  rawobj: Upload a custom JSON schema blob.
  share: Grant access to a resource by making a "share" blob.
  blob: Upload raw blob(s).

Examples:

  camput file [opts] <file(s)/director(ies)
  camput file --permanode --name='Homedir backup' --tag=backup,homedir $HOME
  camput file --filenodes /mnt/camera/DCIM

  camput blob <files>     (raw, without any metadata)
  camput blob -           (read from stdin)

  camput permanode                                (create a new permanode)
  camput permanode -name="Some Name" -tag=foo,bar (with attributes added)

  camput init
  camput init --gpgkey=XXXXX

  camput share [opts] <blobref to share via haveref>

  camput rawobj (debug command)

  camput attr <permanode> <name> <value>         Set attribute
  camput attr --add <permanode> <name> <value>   Adds attribute (e.g. "tag")
  camput attr --del <permanode> <name> [<value>] Deletes named attribute [value

For mode-specific help:

  camput <mode> -help

Global options:
  -help=false: print usage
  -secret-keyring="~/.gnupg/secring.gpg": GnuPG secret keyring file to use.
  -server="": Camlistore server prefix. If blank, the default from the "server" field of
  ~/.camlistore/config is used.
  Acceptable forms: https://you.example.com, example.com:1345 (https assumed),
  or http://you.example.com/alt-root
  -verbose=false: extra debug logging
  -verbose_http=false: show HTTP request summaries
  -version=false: show version
*/
package main
