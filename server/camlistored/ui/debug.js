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

// CamliHome namespace to contain the global vars
var CamliHome = {};

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

function handleFormSearch(e) {
    e.stopPropagation();
    e.preventDefault();

    var input = document.getElementById("inputSearch");
    var btn = document.getElementById("btnSearch");

    if (input.value == "") {
        return;
    }

    var query = input.value.split(/\s*,\s*/);
    window.location = "./search.html?q=" + query[0] + "&t=tag";
}

function indexOnLoad(e) {
    var btnNew = document.getElementById("btnNew");
    if (!btnNew) {
        alert("missing btnNew");
    }
    btnNew.addEventListener("click", btnCreateNewPermanode);
    camliGetRecentlyUpdatedPermanodes({ success: indexBuildRecentlyUpdatedPermanodes });
    var formSearch = document.getElementById("formSearch");
    if (!formSearch) {
        alert("missing formSearch");
    }
    formSearch.addEventListener("submit", handleFormSearch);

    if (disco && disco.uploadHelper) {
        var uploadForm = document.getElementById("uploadform");
        uploadForm.action = disco.uploadHelper;
        document.getElementById("fileinput").disabled = false;
        document.getElementById("filesubmit").disabled = false;
    }
}

function indexBuildRecentlyUpdatedPermanodes(searchRes) {
    var ul = document.getElementById("recent");
    ul.innerHTML = "";
    for (var i = 0; i < searchRes.recent.length; i++) {
        var result = searchRes.recent[i];      
        var li = document.createElement("li");
        var alink = document.createElement("a");
        alink.href = "./?p=" + result.blobref;
        setTextContent(alink, camliBlobTitle(result.blobref, searchRes));
        li.appendChild(alink);
        ul.appendChild(li);
    }
}

window.addEventListener("load", indexOnLoad);
