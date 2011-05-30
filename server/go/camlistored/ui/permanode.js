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
