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

/*
The pk-mount tool mounts a root directory blob onto the given
mountpoint. The blobref can be given directly or through a share blob
URL. If no root blobref is given, an automatic root is created
instead.

# Mounting

Execute the following commands in a shell to mount a Perkeep directory in your home directory.

	mkdir ~/Perkeep
	pk-mount ~/Perkeep
	cd ~/Perkeep
	ls -C1

	WELCOME.txt
	at
	date
	recent
	roots
	sha1-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
	sha224-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
	tag

# Creating a Root Node

Files and directories are anchored in a root node.  You can view and
create Root nodes in the roots directory.  For example to create a
Photos root node execute the following commands:

	cd ~/Perkeep
	mkdir roots/Photos
	cd roots/Photos
	cp -R ~/Photos/* .

# Accessing Recent Items

A list of recently accessed items are visible in the recent directory.

	cd ~/Perkeep/recent
	ls -C1

	IMG_20171104_193001.jpg
	IMG_20171104_205408_thumbnail.jpg
	IMG_20171208_070038.jpg
	test.txt

# Accessing Content at a specific Point in Time

The at directory contains full instructions in the README.txt file contained within.

# Accessing a specific Node

You can directly access a specific directory by using the full sha224 identifier.

	cd ~/Perkeep
	cd sha224-xxx # where xxx is the full 56 character identifier

# Understanding the schema

As there are various ways to model parent/child relationships in Perkeep, and as
they are represented differently in the FUSE interface and in the web user
interface, here is a summary of the schema used by the FUSE interface.

A directory is a permanode with a camliRoot set to the name of the directory, or
a permanode with a camliNodeType set to the "directory" value, and its title set
to the name of the directory.

A file is a permanode with a camliContent set to the blobRef of a file schema (a
fileRef).

The child of a directory can be, as expected, another directory or file, as
defined above.

Permanode X, representing the file or directory "foo", is the child of permanode
Y, representing the directory "bar", if on permanode Y the attribute
"camliPath:foo" is set with the blobRef of permanode X as value.

Full Command Line Usage

	pk-mount [opts] [<mountpoint> [<root-blobref>|<share URL>|<root-name>]]
	-debug
	      print debugging messages.
	-help
	      print usage
	-legal
	      show licenses
	-open
	      Open a GUI window
	-secret-keyring string
	      GnuPG secret keyring file to use.
	-server string
	      Perkeep server prefix.
	      If blank, the default from the "server" field of the default
	      config is used. Acceptable forms:
	         https://you.example.com,
	         example.com:1345 (https assumed), or
	         http://you.example.com/alt-root
	-term
	      Open a terminal window. Doesn't shut down when exited. Mostly for demos.
	-verbose
	      extra debug logging
	-version
	      show version
	-xterm
	      Run an xterm in the mounted directory. Shut down when xterm ends.
*/
package main // import "perkeep.org/cmd/pk-mount"
