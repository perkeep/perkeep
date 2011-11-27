/*
Copyright 2011 Google Inc.

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
Package index provides a generic indexing system on top of an abstract
index storage system (IndexStorage).

The following keys & values are populated by receiving blobs and queried
for search operations:

* Recent Permanodes
  "recpn:<pgp-keyid>:<reverse-modtime>:<claim-blobref>" == "<permanode-blobref>"
   where reverse-modtime flips each digit to '9'-<digit> and prepends "rt" (for reverse time)
          "2011-11-27T01:23:45Z"
    ==> "rt7988-88-72T98:76:54Z"

 * signer blobref of ascii public key -> gpg key id
   "signerkeyid:sha1-ad87ca5c78bd0ce1195c46f7c98e6025abbaf007" = "2931A67C26F5ABDA"

*/
package index
