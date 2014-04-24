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
The camtool binary is a collection of commands to help with the use of
a camlistore server. Notably, it can initialize a database for the
indexer, and it can sync blobs between blobservers.

Usage:

  camtool [globalopts] <mode> [commandopts] [commandargs]

Modes:

  env: Return Camlistore environment information
  googinit: Init Google Drive or Google Cloud Storage.
  list: List blobs on a server.
  claims: Ask the search system to list the claims that modify a permanode.
  dumpconfig: Dump the low-level server config from its simple config.
  describe: Ask the search system to describe one or more blobs.
  discovery: Perform configuration discovery against a server.
  reindex-diskpacked: Rebuild the index of the diskpacked blob store
  index: Synchronize blobs for all discovered blobs storage - indexer pairs.
  sync: Synchronize blobs from a source to a destination.
  dbinit: Set up the database for the indexer.
  debug: Show misc meta-info from the given file.

Examples:

  camtool sync --all
  camtool sync --src http://localhost:3179/bs/ --dest http://localhost:3179/index-mem/

  camtool dbinit -user root -password root -host localhost -dbname camliprod -wipe

For mode-specific help:

  camtool <mode> -help

Global options:
  -help=false: print usage
  -verbose=false: extra debug logging
  -version=false: show version
*/
package main
