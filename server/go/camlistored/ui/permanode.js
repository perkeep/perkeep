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

function handleFormSubmit(e) {
    e.stopPropagation();
    e.preventDefault();

    var inputName = document.getElementById("inputName");
    inputName.disabled = "disabled";
    var btnSave = document.getElementById("btnSave");
    btnSave.disabled = "disabled";

    var startTime = new Date();

    camliNewSetAttributeClaim(
        getPermanodeParam(),
        "name",
        inputName.value,
        {
            success: function() {
                var elapsedMs = new Date().getTime() - startTime.getTime();
                setTimeout(function() {
                    inputName.disabled = null;
                    btnSave.disabled = null;
                }, Math.max(250 - elapsedMs, 0));
            },
            fail: function(msg) {
                alert(msg);
                inputName.disabled = null;
                btnSave.disabled = null;
            }
        });
}

window.addEventListener("load", function (e) {
    var permanode = getPermanodeParam();
    if (permanode) {
      document.getElementById('permanode').innerText = permanode;
    }

    var form = document.getElementById("form");
    form.addEventListener("submit", handleFormSubmit);

    camliDescribeBlob(permanode, {
        success: function(jres) {
            if (!jres[permanode]) {
                alert("didn't get blob " + permanode);
                return;
            }
            var permanodeObject = jres[permanode].permanode;
            if (!permanodeObject) {
                alert("blob " + permanode + " isn't a permanode");
                return;
            }

            var inputName = document.getElementById("inputName");
            inputName.value =
                (permanodeObject.attr.name && permanodeObject.attr.name.length == 1) ?
                permanodeObject.attr.name[0] :
                "";
            inputName.disabled = null;

            var btnSave = document.getElementById("btnSave");
            btnSave.disabled = null;
        },
        failure: function(msg) { alert("failed to get blob description: " + msg); }
    });
});
