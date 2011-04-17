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

var disco = null;

// Method 1 to get discovery information (JSONP style):
function onConfiguration(conf) {
    disco = conf;
    console.log("Got config: " + JSON.stringify(conf));
}

// Or get configuration info like this:
function discover() {
    var xhr = new XMLHttpRequest();
    xhr.onreadystatechange = function() {
        if (xhr.readyState != 4) { return; }
        if (xhr.status != 200) {
            console.log("no status 200; got " + xhr.status);
            return;
        }
        disco = JSON.parse(xhr.responseText);
        document.getElementById("discores").innerHTML = JSON.stringify(disco);
    };
    xhr.open("GET", "./?camli.mode=config", true);
    xhr.send();
}

function search() {
        var xhr = new XMLHttpRequest();
    xhr.onreadystatechange = function() {
        if (xhr.readyState != 4) { return; }
        if (xhr.status != 200) {
            console.log("no status 200; got " + xhr.status);
            return;
        }
        document.getElementById("searchres").innerHTML = "<pre>" + xhr.responseText + "</pre>";
    };
    xhr.open("GET", disco.searchRoot + "camli/search", true);
    xhr.send();
}
