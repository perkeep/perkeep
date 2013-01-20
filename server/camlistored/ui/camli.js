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
if (!window.Camli) {
   window.Camli = {};
}

function $(id) {
    return document.getElementById(id);
}

// innerText is not W3C compliant and does not work with firefox.
// textContent does not work with IE.
// setTextContent should work with all browsers.
Camli.setTextContent = function(ele, text) {
    if ("textContent" in ele) {
        ele.textContent = text;
        return;
    }
    if ("innerText" in ele) {
        ele.innerText = text;
        return;
    }
    while (element.firstChild !== null) {
        element.removeChild(element.firstChild);
    }
    element.appendChild(document.createTextNode(text));
};

// Method 1 to get discovery information (JSONP style):
Camli.onConfiguration = function(config) {
    Camli.config = config;
};

Camli.saneOpts = function(opts) {
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
};

// Format |dateVal| as specified by RFC 3339.
Camli.dateToRfc3339String = function(dateVal) {
    // Return a string containing |num| zero-padded to |length| digits.
    var pad = function(num, length) {
        var numStr = "" + num;
        while (numStr.length < length) {
            numStr = "0" + numStr;
        }
        return numStr;
    };
    return dateVal.getUTCFullYear() + "-" + pad(dateVal.getUTCMonth() + 1, 2) + "-" + pad(dateVal.getUTCDate(), 2) + "T" +
           pad(dateVal.getUTCHours(), 2) + ":" + pad(dateVal.getUTCMinutes(), 2) + ":" + pad(dateVal.getUTCSeconds(), 2) + "Z";
};

function camliDescribeBlob(blobref, opts) {
    var xhr = camliJsonXhr("camliDescribeBlob", opts);
    var path = Camli.config.searchRoot +
            "camli/search/describe?blobref=" + blobref;
    if (opts.thumbnails != null) {
        path = Camli.makeURL(path, {thumbnails: opts.thumbnails});
    }
    xhr.open("GET", path, true);
    xhr.send();
}

Camli.makeURL = function(base, map) {
    for (var key in map) {
        if (base.indexOf("?") == -1) {
            base += "?";
        } else {
            base += "&";
        }
        base += key + "=" + encodeURIComponent(map[key]);
    }
    return base;
};

function camliPermanodeOfSignerAttrValue(signer, attr, value, opts) {
    var xhr = camliJsonXhr("camliPermanodeOfSignerAttrValue", opts);
    var path = Camli.makeURL(Camli.config.searchRoot + "camli/search/signerattrvalue",
                       { signer: signer, attr: attr, value: value });
    xhr.open("GET", path, true);
    xhr.send();
}

// Where is the target accessed via? (paths it's at)
function camliPathsOfSignerTarget(signer, target, opts) {
    var xhr = camliJsonXhr("camliPathsOfSignerTarget", opts);
    var path = Camli.makeURL(Camli.config.searchRoot + "camli/search/signerpaths",
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
    opts = Camli.saneOpts(opts);
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
    opts = Camli.saneOpts(opts);
    var sigConf = Camli.config.signing;
    if (!sigConf || !sigConf.publicKeyBlobRef) {
       camliCondCall(opts.fail, "Missing Camli.config.signing.publicKeyBlobRef");
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
}

// camliUploadFile uploads a file and returns a file schema. It does not create
// any permanodes.
//
// file: File object
// opts: optional callbacks:
// opts:
//   - fail: function(msg)
//   - success: function(fileBlobRef) of the server-validated or
//         just-uploaded file schema blob.
//   - onContentsRef: function(blobref) of contents, once hashed in-browser
function camliUploadFile(file, opts) {
    var fr = new FileReader();
    fr.onload = function() {
        var dataurl = fr.result;
        var comma = dataurl.indexOf(",");
        if (comma != -1) {
            var b64 = dataurl.substring(comma + 1);
            var arrayBuffer = Base64.decode(b64).buffer;
            var hash = Crypto.SHA1(new Uint8Array(arrayBuffer, 0));

            var contentsRef = "sha1-" + hash;
            camliCondCall(opts.onContentsRef, contentsRef);
            camliUploadFileHelper(file, contentsRef, {
                success: opts.success, fail: opts.fail
            });
        }
    };
    fr.onerror = function() {
        console.log("FileReader onerror: " + fr.error + " code=" + fr.error.code);
    };
    fr.readAsDataURL(file);
}

// camliUploadFileHelper uploads the provided file with contents blobref contentsBlobRef
// and returns a blobref of a file blob.  It does not create any permanodes.
// Most callers will use camliUploadFile instead of this helper.
//
// camliUploadFileHelper only uploads chunks of the file if they don't already exist
// on the server. It starts by assuming the file might already exist on the server
// and, if so, uses an existing (but re-verified) file schema ref instead.
//
// file: File object
// contentsBlobRef: blob ref of file as sha1'd locally
// opts:
//   - fail: function(msg)
//   - success: function(fileBlobRef) of the server-validated or
//         just-uploaded file schema blob.
function camliUploadFileHelper(file, contentsBlobRef, opts) {
    opts = Camli.saneOpts(opts);
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
            var checkFile = remain.shift();
            console.log("integrity checking the reported dup " + checkFile);

            var vcb = {};
            vcb.fail = function(xhr) {
                console.log("integrity checked failed on " + checkFile);
                checkNext();
            };
            vcb.success = function(xhr) {
                if (xhr.getResponseHeader("X-Camli-Contents") == contentsBlobRef) {
                    console.log("integrity checked passed on " + checkFile + "; using it.");
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
    opts = Camli.saneOpts(opts);
    var blobref = "sha1-" + Crypto.SHA1(s);
    var parts = [s];

    var bb = new Blob(parts);

    var fd = new FormData();
    fd.append(blobref, bb);

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
    opts = Camli.saneOpts(opts);
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
Camli.getQueryParam = function(key) {
    var params = document.location.search.substring(1).split('&');
    for (var i = 0; i < params.length; ++i) {
        var parts = params[i].split('=');
        if (parts.length == 2 && decodeURIComponent(parts[0]) == key)
            return decodeURIComponent(parts[1]);
    }
    return null;
};

function camliGetRecentlyUpdatedPermanodes(opts) {
    // opts.thumbnails is the maximum size of the thumbnails we want,
    // or 0 if no thumbnail.
    var path = Camli.config.searchRoot + "camli/search/recent";
    if (opts.thumbnails != null) {
        path = Camli.makeURL(path, {thumbnails: opts.thumbnails});
    }
    var xhr = camliJsonXhr("camliGetRecentlyUpdatedPermanodes", opts);
    xhr.open("GET", path, true);
    xhr.send();
}

function camliGetPermanodesWithAttr(signer, attr, value, fuzzy, opts) {
    var xhr = camliJsonXhr("camliGetPermanodesWithAttr", opts);
    var path = Camli.makeURL(Camli.config.searchRoot + "camli/search/permanodeattr",
                       { signer: signer, attr: attr, value: value, fuzzy: fuzzy });
    xhr.open("GET", path, true);
    xhr.send();
}

function camliXhr(name, opts) {
    opts = Camli.saneOpts(opts);
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
    opts = Camli.saneOpts(opts);
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

function camliFindExistingFileSchemas(wholeDigestRef, opts) {
    var xhr = camliJsonXhr("camliFindExistingFileSchemas", opts);
    var path = Camli.config.searchRoot + "camli/search/files?wholedigest=" +
        wholeDigestRef;
    xhr.open("GET", path, true);
    xhr.send();
}

// Returns true if the passed-in string might be a blobref.
Camli.isPlausibleBlobRef = function(blobRef) {
    return /^\w+-[a-f0-9]+$/.test(blobRef);
};

Camli.linkifyBlobRefs = function(schemaBlob) {
    var re = /(\w{3,6}-[a-f0-9]{30,})/g;
    return schemaBlob.replace(re, "<a href='./?b=$1'>$1</a>");
};

// Helper function for camliNewSetAttributeClaim() (and eventually, for
// similar functions to add or delete attributes).
Camli.changeAttribute = function(permanode, claimType, attribute, value, opts) {
    opts = Camli.saneOpts(opts);
    var json = {
        "camliVersion": 1,
        "camliType": "claim",
        "permaNode": permanode,
        "claimType": claimType,
        "claimDate": Camli.dateToRfc3339String(new Date()),
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
};

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
    if (d.camliType == "directory" && d.dir && d.dir.fileName) {
        return d.dir.fileName;
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
    Camli.changeAttribute(permanode, "set-attribute", attribute, value, opts);
}

// Create and upload a new add-attribute claim.
function camliNewAddAttributeClaim(permanode, attribute, value, opts) {
    Camli.changeAttribute(permanode, "add-attribute", attribute, value, opts);
}

// Create and upload a new del-attribute claim.
function camliNewDelAttributeClaim(permanode, attribute, value, opts) {
    Camli.changeAttribute(permanode, "del-attribute", attribute, value, opts);
}

// camliCondCall calls fn, if non-null, with the remaining parameters.
function camliCondCall(fn /*, ... */) {
    if (!fn) {
        return;
    }
    fn.apply(null, Array.prototype.slice.call(arguments, 1));
}
