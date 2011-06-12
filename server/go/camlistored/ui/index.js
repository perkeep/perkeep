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

function btnCreateNewPermanode(e) {
    camliCreateNewPermanode(
        {
            success: function(blobref) {
               window.location = "./?p=" + blobref;
            },
            fail: function(msg) {
                alert("create permanode failed: " + msg);
            }
        });
}

function indexOnLoad(e) {
    var btnNew = document.getElementById("btnNew");
    if (!btnNew) {
        alert("missing btnNew");
    }
    btnNew.addEventListener("click", btnCreateNewPermanode);
    camliGetRecentlyUpdatedPermanodes({ success: indexBuildRecentlyUpdatedPermanodes });

    if (disco && disco.uploadHelper) {
        var uploadForm = document.getElementById("uploadform");
        uploadform.action = disco.uploadHelper;
        document.getElementById("fileinput").disabled = false;
        document.getElementById("filesubmit").disabled = false;
        var chkRollSum = document.getElementById("chkrollsum");
        chkRollSum.addEventListener("change", function (e) {
                                        if (chkRollSum.checked) {
                                            if (disco.uploadHelper.indexOf("?") == -1) {
                                                uploadform.action = disco.uploadHelper + "?rollsum=1";
                                            } else {
                                                uploadform.action = disco.uploadHelper + "&rollsum=1";
                                            }
                                        } else {
                                            uploadform.action = disco.uploadHelper;
                                        }
                                    });
    }

}

function indexBuildRecentlyUpdatedPermanodes(searchRes) {
    var div = document.getElementById("recent");
    div.innerHTML = "";
    for (var i = 0; i < searchRes.recent.length; i++) {
        var result = searchRes.recent[i];      
        var pdiv = document.createElement("li");
        var alink = document.createElement("a");
        alink.href = "./?p=" + result.blobref;
        alink.innerText = camliBlobTitle(result.blobref, searchRes);
        pdiv.appendChild(alink);
        div.appendChild(pdiv);
    }
}

window.addEventListener("load", indexOnLoad);
