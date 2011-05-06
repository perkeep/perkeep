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

var sigdisco = null;

function discoverJsonSign() {
    var xhr = new XMLHttpRequest();
    xhr.onreadystatechange = function() {
        if (xhr.readyState != 4) { return; }
        if (xhr.status != 200) {
            console.log("no status 200; got " + xhr.status);
            return;
        }
        sigdisco = JSON.parse(xhr.responseText);
        document.getElementById("sigdiscores").innerHTML = JSON.stringify(sigdisco);
    };
    xhr.open("GET", disco.jsonSignRoot + "/camli/sig/discovery", true);
    xhr.send();
}

function addKeyRef() {
    if (!sigdisco) {
        alert("must do jsonsign discovery first");        
        return;
    }
    clearta = document.getElementById("clearjson");
    var j;
    try {
        j = JSON.parse(clearta.value);
    } catch (x) {
        alert(x);
        return
    }
    j.camliSigner = sigdisco.publicKeyBlobRef;
    clearta.value = JSON.stringify(j);
}

function doSign() {
    if (!sigdisco) {
        alert("must do jsonsign discovery first");
        return;
    }
    clearta = document.getElementById("clearjson");

    var xhr = new XMLHttpRequest();
    xhr.onreadystatechange = function() {
        if (xhr.readyState != 4) { return; }
        if (xhr.status != 200) {
            alert("got status " + xhr.status)
            return;
        }
        document.getElementById("signedjson").value = xhr.responseText;
    };
    xhr.open("POST", sigdisco.signHandler, true);
    xhr.setRequestHeader("Content-Type", "application/x-www-form-urlencoded");
    xhr.send("json=" + encodeURIComponent(clearta.value));
}

function doVerify() {
    if (!sigdisco) {
        alert("must do jsonsign discovery first");
        return;
    }

    signedta = document.getElementById("signedjson");

    var xhr = new XMLHttpRequest();
    xhr.onreadystatechange = function() {
        if (xhr.readyState != 4) { return; }
        if (xhr.status != 200) {
            alert("got status " + xhr.status)
            return;
        }
        document.getElementById("verifyinfo").innerHTML = "<pre>" + xhr.responseText + "</pre>";
    };
    xhr.open("POST", sigdisco.verifyHandler, true);
    xhr.setRequestHeader("Content-Type", "application/x-www-form-urlencoded");
    xhr.send("sjson=" + encodeURIComponent(signedta.value));
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
