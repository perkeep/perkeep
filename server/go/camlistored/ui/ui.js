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

function saneOpts(opts) {
    if (!opts) {
        opts = {}
    }
    if (!opts.success) {
        opts.success = function() {};
    }
    if (!opts.fail) {
        opts.fail = function() {};
    }
    return opts;
}

var cachedCamliSigDiscovery;

function camliSigDiscovery(opts) {
    opts = saneOpts(opts);
    if (cachedCamliSigDiscovery) {
        opts.success(cachedCamliSigDiscovery);
        return;
    }
    var xhr = new XMLHttpRequest();
    xhr.onreadystatechange = function() {
        if (xhr.readyState != 4) { return; }
        if (xhr.status != 200) {
            opts.fail("no status 200; got " + xhr.status);
            return;
        }
        sigdisco = JSON.parse(xhr.responseText);
        cachedCamliSigDiscovery = sigdisco;
        opts.success(sigdisco);
    };
    xhr.open("GET", disco.jsonSignRoot + "/camli/sig/discovery", true);
    xhr.send();
}

function camliSign(clearObj, opts) {
    opts = saneOpts(opts);

    camliSigDiscovery(
        {
            success: function(sigConf) {
                if (!sigConf.publicKeyBlobRef) {
                    opts.fail("Missing sigConf.publicKeyBlobRef");
                    return;
                }
                clearObj.camliSigner = sigConf.publicKeyBlobRef;
                clearText = JSON.stringify(clearObj, null, 2);
                
                var xhr = new XMLHttpRequest();
                xhr.onreadystatechange = function() {
                    if (xhr.readyState != 4) { return; }
                    if (xhr.status != 200) {
                        opts.fail("got status " + xhr.status);
                        return;
                    }
                    opts.success(xhr.responseText);
                };
                xhr.open("POST", sigConf.signHandler, true);
                xhr.setRequestHeader("Content-Type", "application/x-www-form-urlencoded");
                xhr.send("json=" + encodeURIComponent(clearText));
            },
            fail: function(errMsg) {
                opts.fail(errMsg);
            }
        });
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

function camliUploadString(s, opts) {
    opts = saneOpts(opts);
    var blobref = "sha1-" + Crypto.SHA1(s);
    // alert("blobref " + blobref + ": " + s);

    bb = new WebKitBlobBuilder();
    bb.append(s);
    
    var fd = new FormData();
    fd.append(blobref, bb.getBlob());
    
    var xhr = new XMLHttpRequest();

    // TODO: hack, hard-coding the upload URL here.
    // Change the spec now that App Engine permits 32 MB requests
    // and permit a PUT request on the sha1?  Or at least let us
    // specify the well-known upload URL?  In cases like this, uploading
    // a new permanode, it's silly to even stat.
    xhr.open("POST", disco.blobRoot + "camli/upload")
    xhr.onreadystatechange = function() {
        if (xhr.readyState != 4) {
             return;
        }
        if (xhr.status != 200) {
            opts.fail("got status " + xhr.status);
            return;
        }
        var resj;
        try {
            resj = JSON.parse(xhr.responseText);
        } catch (x) {
            opts.fail("error parsing JSON in upload response: " + xhr.responseText);
            return;
        }
        if (resj.errorText) {
            opts.fail("error uploading " + blobref + ": " + resj.errorText);
            return;
        }
        // TODO: check resj.received[] array.
        opts.success(blobref);
    };
    xhr.send(fd);
}

function createNewPermanode() {
     var json = {
         "camliVersion": 1,
         "camliType": "permanode",
         "random": ""+Math.random()
     };
     camliSign(json, {
                   success: function(got) {
                       camliUploadString(
                           got,
                           {
                               success: function(blobref) {
                                   alert("uploaded permanode blobref: " + blobref);
                               },
                               fail: function(msg) {
                                   alert("upload permanode fail: " + msg);                                   
                               }
                           })
                   },
                   fail: function(msg) {
                       alert("sign fail: " + msg);
                   }
               });
}

function camliOnload(e) {
    var btnNew = document.getElementById("btnNew");
    if (btnNew) {
        btnNew.addEventListener("click", createNewPermanode);
    }
}

window.addEventListener("load", camliOnload);