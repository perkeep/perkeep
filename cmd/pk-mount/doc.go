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

Mounting

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
  tag


Creating a Root Node

Files and directories are anchored in a root node.  You can view and
create Root nodes in the roots directory.  For example to create a
Photos root node execute the following commands:

  cd ~/Perkeep
  mkdir roots/Photos
  cd roots/Photos
  cp -R ~/Photos/* .


Accessing Recent Items

A list of recently accessed items are visible in the recent directory.

  cd ~/Perkeep/recent
  ls -C1

  IMG_20171104_193001.jpg
  IMG_20171104_205408_thumbnail.jpg
  IMG_20171208_070038.jpg
  test.txt


Accessing Content at a specific Point in Time

The at directory contains full instructions in the README.txt file contained within.


Accessing a specific Node

You can directly access a specific directory by using the full sha1 identifier.

   cd ~/Perkeep
   cd sha1-xxx # where xxx is the full 16 character identifier


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
