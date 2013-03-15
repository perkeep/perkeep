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
The cammount tool mounts a root directory blob onto the given mountpoint. The blobref can be given directly or through a share blob URL. If no root blobref is given, an automatic root is created instead.


Usage:

	cammount [opts] <mountpoint> [<root-blobref>|<share URL>]
	-debug=false: print debugging messages.
	-server="": Camlistore server prefix.
	If blank, the default from the "server" field of ~/.camlistore/config is used.
	Acceptable forms: https://you.example.com, example.com:1345 (https assumed), or
	http://you.example.com/alt-root
*/
package main
