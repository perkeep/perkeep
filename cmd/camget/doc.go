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
The camget tool fetches blobs, files, and directories.


Examples

Writes to stdout by default:

  camget <blobref>                 // dump raw blob
  camget -contents <file-blobref>  // dump file contents

Like curl, lets you set output file/directory with -o:

  camget -o <dir> <blobref>
    (if <dir> exists and is directory, <blobref> must be a directory;
     use -f to overwrite any files)

  camget -o <filename> <file-blobref>

Camget isn't very fleshed out. In general, using 'cammount' to just
mount a tree is an easier way to get files back.
*/
package main
