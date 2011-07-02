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

// Camli namespace.
var Camli = {};

var disco = null;  // TODO: kill this in favor of Camli.config.

// Method 1 to get discovery information (JSONP style):
function onConfiguration(config) {
    Camli.config = disco = config;
    console.log("Got config: " + JSON.stringify(config));
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

// Format |dateVal| as specified by RFC 3339.
function dateToRfc3339String(dateVal) {
    // Return a string containing |num| zero-padded to |length| digits.
    var pad = function(num, length) {
        var numStr = "" + num;
        while (numStr.length < length) {
            numStr = "0" + numStr;
        }
        return numStr;
    }
    return dateVal.getUTCFullYear() + "-" + pad(dateVal.getUTCMonth() + 1, 2) + "-" + pad(dateVal.getUTCDate(), 2) + "T" +
           pad(dateVal.getUTCHours(), 2) + ":" + pad(dateVal.getUTCMinutes(), 2) + ":" + pad(dateVal.getUTCSeconds(), 2) + "Z";
}

var cachedCamliSigDiscovery;

// opts.success called with discovery object
// opts.fail called with error text
function camliSigDiscovery(opts) {
    opts = saneOpts(opts);
    if (cachedCamliSigDiscovery) {
        opts.success(cachedCamliSigDiscovery);
        return;
    }
    var cb = {};
    cb.success = function(sd) {
      cachedCamliSigDiscovery = sd;
      opts.success(sd);
    };
    cb.fail = opts.fail;
    var xhr = camliJsonXhr("camliDescribeBlob", cb);
    xhr.open("GET", Camli.config.jsonSignRoot + "/camli/sig/discovery", true);
    xhr.send();
}

function camliDescribeBlob(blobref, opts) {
    var xhr = camliJsonXhr("camliDescribeBlob", opts);
    var path = Camli.config.searchRoot + "camli/search/describe?blobref=" +
        blobref;
    xhr.open("GET", path, true);
    xhr.send();
}

function makeURL(base, map) {
    for (var key in map) {
        if (base.indexOf("?") == -1) {
            base += "?";
        } else {
            base += "&";
        }
        base += key + "=" + encodeURIComponent(map[key]);
    }
    return base;
}

function camliPermanodeOfSignerAttrValue(signer, attr, value, opts) {
    var xhr = camliJsonXhr("camliPermanodeOfSignerAttrValue", opts);
    var path = makeURL(Camli.config.searchRoot + "camli/search/signerattrvalue",
                       { signer: signer, attr: attr, value: value });
    xhr.open("GET", path, true);
    xhr.send();
}

// Where is the target accessed via? (paths it's at)
function camliPathsOfSignerTarget(signer, target, opts) {
    var xhr = camliJsonXhr("camliPathsOfSignerTarget", opts);
    var path = makeURL(Camli.config.searchRoot + "camli/search/signerpaths",
                           { signer: signer, target: target });
    xhr.open("GET", path, true);
    xhr.send();
}

function camliGetPermanodeClaims(permanode, opts) {
    var xhr = camliJsonXhr("camliGetPermanodeClaims", opts);
    var path = Camli.config.searchRoot + "camli/search/claims?permanode=" +
        permanode;
    xhr.open("GET", path, true);
    xhr.send();
}

function camliGetBlobContents(blobref, opts) {
    opts = saneOpts(opts);
    var xhr = new XMLHttpRequest();
    xhr.onreadystatechange = function() {
        if (xhr.readyState != 4) { return; }
        if (xhr.status != 200) {
            opts.fail("camliGetBlobContents HTTP status " + xhr.status);
            return;
        }
        opts.success(xhr.responseText);
    };
    xhr.open("GET", camliBlobURL(blobref), true);
    xhr.send();
}

function camliBlobURL(blobref) {
    return Camli.config.blobRoot + "camli/" + blobref;
}

function camliDescribeBlogURL(blobref) {
    return Camli.config.searchRoot + 'camli/search/describe?blobref=' + blobref;
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

// file: File object
// contentsBlobRef: blob ref of file as sha1'd locally
// opts: fail(strMsg) success(strFileBlobRef) of the validated (or uploaded + created) file schema blob.
//       associating with a permanode is caller's job.
function camliUploadFileHelper(file, contentsBlobRef, opts) {
    opts = saneOpts(opts);
    if (!Camli.config.uploadHelper) {
        opts.fail("no uploadHelper available");
        return
    }

    var doUpload = function() {
        var fd = new FormData();
        fd.append(fd, file);
        var uploadCb = { fail: opts.fail };
        uploadCb.success = function(res) {
            if (res.got && res.got.length == 1 && res.got[0].fileref) {
                var fileblob = res.got[0].fileref;
                console.log("uploaded " + contentsBlobRef + " => file blob " + fileblob);
                opts.success(fileblob);
            } else {
                opts.fail("failed to upload " + file.name + ": " + contentsBlobRef + ": " + JSON.stringify(res, null, 2))
            }
        };
        var xhr = camliJsonXhr("camliUploadFileHelper", uploadCb);
        xhr.open("POST", Camli.config.uploadHelper);
        xhr.send(fd);
    };

    var dupcheckCb = { fail: opts.fail };
    dupcheckCb.success = function(res) {
        var remain = res.files;
        var checkNext;
        checkNext = function() {
            if (remain.length == 0) {
                doUpload();
                return;
            }
            // TODO: verify filename and other file metadata in the
            // file json schema match too, not just the contents
            var checkFile = remain.shift()
            console.log("integrity checking reported dup " + checkFile);

            var vcb = {};
            vcb.fail = function(xhr) {
                console.log("integrity checked failed on " + checkFile);
                checkNext();
            };
            vcb.success = function(xhr) {
                if (xhr.getResponseHeader("X-Camli-Contents") == contentsBlobRef) {
                    console.log("integrity checked passed on " + checkFile);
                    opts.success(checkFile);
                } else {
                    checkNext();
                }
            };
            var xhr = camliXhr("headVerifyFile", vcb);
            xhr.open("HEAD", Camli.config.downloadHelper + checkFile + "/?verifycontents=" + contentsBlobRef, true);
            xhr.send();
        };
        checkNext();
    };
    camliFindExistingFileSchemas(contentsBlobRef, dupcheckCb);
}

function camliUploadString(s, opts) {
    opts = saneOpts(opts);
    var blobref = "sha1-" + Crypto.SHA1(s);

    bb = new WebKitBlobBuilder();
    bb.append(s);

    var fd = new FormData();
    fd.append(blobref, bb.getBlob());

    var uploadCb = {};
    uploadCb.success = function(resj) {
        // TODO: check resj.received[] array.
        opts.success(blobref);
    };
    uploadCb.fail = opts.fail;
    var xhr = camliJsonXhr("camliUploadString", uploadCb);
    // TODO: hack, hard-coding the upload URL here.
    // Change the spec now that App Engine permits 32 MB requests
    // and permit a PUT request on the sha1?  Or at least let us
    // specify the well-known upload URL?  In cases like this, uploading
    // a new permanode, it's silly to even stat.
    xhr.open("POST", Camli.config.blobRoot + "camli/upload")
    xhr.send(fd);
}

function camliCreateNewPermanode(opts) {
    opts = saneOpts(opts);
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
                               success: opts.success,
                               fail: function(msg) {
                                   opts.fail("upload permanode fail: " + msg);
                               }
                           });
                   },
                   fail: function(msg) {
                       opts.fail("sign permanode fail: " + msg);
                   }
               });
}

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

function camliGetRecentlyUpdatedPermanodes(opts) {
    var xhr = camliJsonXhr("camliGetRecentlyUpdatedPermanodes", opts);
    xhr.open("GET", Camli.config.searchRoot + "camli/search/recent", true);
    xhr.send();
}

function camliGetTaggedPermanodes(signer, value, opts) {
    var xhr = camliJsonXhr("camliGetTaggedPermanodes", opts);
    var path = makeURL(Camli.config.searchRoot + "camli/search/tag",
                       { signer: signer, value: value });
    xhr.open("GET", path, true);
    xhr.send();
}

function camliXhr(name, opts) {
    opts = saneOpts(opts);
    var xhr = new XMLHttpRequest();
    xhr.onreadystatechange = function() {
        if (xhr.readyState != 4) { return; }
        if (xhr.status == 200) {
            opts.success(xhr);
        } else {
            opts.fail(name + ": expected status 200; got " + xhr.status + ": " + xhr.responseText);
        }
    };
    return xhr;
}

function camliJsonXhr(name, opts) {
    opts = saneOpts(opts);
    var xhr = new XMLHttpRequest();
    xhr.onreadystatechange = function() {
        if (xhr.readyState != 4) { return; }
        if (xhr.status != 200) {
            try {
                var resj = JSON.parse(xhr.responseText);
                opts.fail(name + ": expected status 200; got " + xhr.status + ": " + resj.error);
            } catch(x) {
                opts.fail(name + ": expected status 200; got " + xhr.status + ": " + xhr.responseText);
            }
            return;
        }
        var resj;
        try {
            resj = JSON.parse(xhr.responseText);
        } catch(x) {
            opts.fail(name + ": error parsing JSON in response: " + xhr.responseText);
            return
        }
        if (resj.error) {
            opts.fail(resj.error);
        } else {
            opts.success(resj);
        }
    };
    return xhr;
}

function camliFindExistingFileSchemas(bytesRef, opts) {
    var xhr = camliJsonXhr("camliFindExistingFileSchemas", opts);
    var path = Camli.config.searchRoot + "camli/search/files?bytesref=" +
        bytesRef;
    xhr.open("GET", path, true);
    xhr.send();
}

// Returns true if the passed-in string might be a blobref.
function isPlausibleBlobRef(blobRef) {
    return /^\w+-[a-f0-9]+$/.test(blobRef);
}

function linkifyBlobRefs(schemaBlob) {
    var re = /(\w{3,6}-[a-f0-9]{30,})/g;
    return schemaBlob.replace(re, "<a href='./?b=$1'>$1</a>");
}

// Helper function for camliNewSetAttributeClaim() (and eventually, for
// similar functions to add or delete attributes).
function changeAttribute(permanode, claimType, attribute, value, opts) {
    opts = saneOpts(opts);
    var json = {
        "camliVersion": 1,
        "camliType": "claim",
        "permaNode": permanode,
        "claimType": claimType,
        "claimDate": dateToRfc3339String(new Date()),
        "attribute": attribute,
        "value": value
    };
    camliSign(json, {
        success: function(signedBlob) {
            camliUploadString(signedBlob, {
                success: opts.success,
                fail: function(msg) {
                    opts.fail("upload " + claimType + " fail: " + msg);
                }
            });
        },
        fail: function(msg) {
            opts.fail("sign " + claimType + " fail: " + msg);
        }
    });
}

// pn: permanode to find a good title of
// jdes: describe response of root permanode
function camliBlobTitle(pn, des) {
    var d = des[pn];
    if (!d) {
        return pn;
    }
    if (d.camliType == "file" && d.file && d.file.fileName) {
        return d.file.fileName;
    }
    if (d.permanode) {
        var attr = d.permanode.attr;
        if (!attr) {
            return pn;
        }
        if (attr.title) {
            return attr.title[0];
        }
        if (attr.camliContent) {
            return camliBlobTitle(attr.camliContent[0], des);
        }
    }
    return pn;
}

// Create and upload a new set-attribute claim.
function camliNewSetAttributeClaim(permanode, attribute, value, opts) {
    changeAttribute(permanode, "set-attribute", attribute, value, opts);
}

// Create and upload a new add-attribute claim.
function camliNewAddAttributeClaim(permanode, attribute, value, opts) {
    changeAttribute(permanode, "add-attribute", attribute, value, opts);
}

// Create and upload a new del-attribute claim.
function camliNewDelAttributeClaim(permanode, attribute, value, opts) {
    changeAttribute(permanode, "del-attribute", attribute, value, opts);
}

