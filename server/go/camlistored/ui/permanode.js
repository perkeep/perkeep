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

// Returns the first value from the query string corresponding to |key|.
// Returns null if the key isn't present.
function getQueryParam(key) {
  var params = document.location.search.substring(1).split('&');
  for (var i = 0; i < params.length; ++i) {
    var parts = params[i].split('=');
    if (parts.length == 2 && decodeURIComponent(parts[0]) == key)
      return decodeURIComponent(parts[1]);
  }
  return null;
}

// Returns true if the passed-in string might be a blobref.
function isPlausibleBlobRef(blobRef) {
  return /^\w+-[a-f0-9]+$/.test(blobRef);
}

// Gets the |p| query parameter, assuming that it looks like a blobref.
function getPermanodeParam() {
  var blobRef = getQueryParam('p');
  return (blobRef && isPlausibleBlobRef(blobRef)) ? blobRef : null;
}

window.addEventListener("load", function (e) {
      var permanode = getPermanodeParam();
      if (permanode) {
        document.getElementById('permanode').innerText = permanode;
      }
});
