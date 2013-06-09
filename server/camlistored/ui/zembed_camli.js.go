// THIS FILE IS AUTO-GENERATED FROM camli.js
// DO NOT EDIT.

package ui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("camli.js", 17391, time.Unix(0, 1369518799000000000), fileembed.String("/*\n"+
		"Copyright 2011 Google Inc.\n"+
		"\n"+
		"Licensed under the Apache License, Version 2.0 (the \"License\");\n"+
		"you may not use this file except in compliance with the License.\n"+
		"You may obtain a copy of the License at\n"+
		"\n"+
		"     http://www.apache.org/licenses/LICENSE-2.0\n"+
		"\n"+
		"Unless required by applicable law or agreed to in writing, software\n"+
		"distributed under the License is distributed on an \"AS IS\" BASIS,\n"+
		"WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.\n"+
		"See the License for the specific language governing permissions and\n"+
		"limitations under the License.\n"+
		"*/\n"+
		"\n"+
		"if (typeof goog != 'undefined' && typeof goog.provide != 'undefined') {\n"+
		"    goog.provide('camlistore.CamliCommon');\n"+
		"\n"+
		"    goog.require('camlistore.base64');\n"+
		"    goog.require('camlistore.SHA1');\n"+
		"    goog.require('camlistore.ServerType');\n"+
		"}\n"+
		"\n"+
		"// Camli namespace.\n"+
		"if (!window.Camli) {\n"+
		"   window.Camli = {};\n"+
		"}\n"+
		"\n"+
		"/**\n"+
		" * @typedef {{\n"+
		" *   success: Function?,\n"+
		" *   fail: Function?\n"+
		" * }}\n"+
		" */\n"+
		"Camli.OptCallbacks;\n"+
		"\n"+
		"\n"+
		"function $(id) {\n"+
		"    return document.getElementById(id);\n"+
		"}\n"+
		"\n"+
		"// innerText is not W3C compliant and does not work with firefox.\n"+
		"// textContent does not work with IE.\n"+
		"// setTextContent should work with all browsers.\n"+
		"Camli.setTextContent = function(ele, text) {\n"+
		"    if (\"textContent\" in ele) {\n"+
		"        ele.textContent = text;\n"+
		"        return;\n"+
		"    }\n"+
		"    if (\"innerText\" in ele) {\n"+
		"        ele.innerText = text;\n"+
		"        return;\n"+
		"    }\n"+
		"    while (element.firstChild !== null) {\n"+
		"        element.removeChild(element.firstChild);\n"+
		"    }\n"+
		"    element.appendChild(document.createTextNode(text));\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		"* Sets the Camlistore Discovery configuration object.\n"+
		"*\n"+
		"* @param {camlistore.ServerType.DiscoveryDocument} config The Camlistore configur"+
		"ation Object from Discovery.\n"+
		"*\n"+
		"* @expose\n"+
		"*/\n"+
		"Camli.onConfiguration = function(config) {\n"+
		"    Camli.config = config;\n"+
		"};\n"+
		"\n"+
		"Camli.saneOpts = function(opts) {\n"+
		"    if (!opts) {\n"+
		"        opts = {}\n"+
		"    }\n"+
		"    if (!opts.success) {\n"+
		"        opts.success = function() {};\n"+
		"    }\n"+
		"    if (!opts.fail) {\n"+
		"        opts.fail = function() {};\n"+
		"    }\n"+
		"    return opts;\n"+
		"};\n"+
		"\n"+
		"// Format |dateVal| as specified by RFC 3339.\n"+
		"Camli.dateToRfc3339String = function(dateVal) {\n"+
		"    // Return a string containing |num| zero-padded to |length| digits.\n"+
		"    var pad = function(num, length) {\n"+
		"        var numStr = \"\" + num;\n"+
		"        while (numStr.length < length) {\n"+
		"            numStr = \"0\" + numStr;\n"+
		"        }\n"+
		"        return numStr;\n"+
		"    };\n"+
		"    return dateVal.getUTCFullYear() + \"-\" + pad(dateVal.getUTCMonth() + 1, 2) + \""+
		"-\" + pad(dateVal.getUTCDate(), 2) + \"T\" +\n"+
		"           pad(dateVal.getUTCHours(), 2) + \":\" + pad(dateVal.getUTCMinutes(), 2) "+
		"+ \":\" + pad(dateVal.getUTCSeconds(), 2) + \"Z\";\n"+
		"};\n"+
		"\n"+
		"function camliDescribeBlob(blobref, opts) {\n"+
		"    var xhr = camliJsonXhr(\"camliDescribeBlob\", opts);\n"+
		"    var path = Camli.config.searchRoot +\n"+
		"            \"camli/search/describe?blobref=\" + blobref;\n"+
		"    if (opts.thumbnails != null) {\n"+
		"        path = Camli.makeURL(path, {thumbnails: opts.thumbnails});\n"+
		"    }\n"+
		"    xhr.open(\"GET\", path, true);\n"+
		"    xhr.send();\n"+
		"}\n"+
		"\n"+
		"Camli.makeURL = function(base, map) {\n"+
		"    for (var key in map) {\n"+
		"        if (base.indexOf(\"?\") == -1) {\n"+
		"            base += \"?\";\n"+
		"        } else {\n"+
		"            base += \"&\";\n"+
		"        }\n"+
		"        base += key + \"=\" + encodeURIComponent(map[key]);\n"+
		"    }\n"+
		"    return base;\n"+
		"};\n"+
		"\n"+
		"function camliPermanodeOfSignerAttrValue(signer, attr, value, opts) {\n"+
		"    var xhr = camliJsonXhr(\"camliPermanodeOfSignerAttrValue\", opts);\n"+
		"    var path = Camli.makeURL(Camli.config.searchRoot + \"camli/search/signerattrva"+
		"lue\",\n"+
		"                       { signer: signer, attr: attr, value: value });\n"+
		"    xhr.open(\"GET\", path, true);\n"+
		"    xhr.send();\n"+
		"}\n"+
		"\n"+
		"// Where is the target accessed via? (paths it's at)\n"+
		"function camliPathsOfSignerTarget(signer, target, opts) {\n"+
		"    var xhr = camliJsonXhr(\"camliPathsOfSignerTarget\", opts);\n"+
		"    var path = Camli.makeURL(Camli.config.searchRoot + \"camli/search/signerpaths\""+
		",\n"+
		"                           { signer: signer, target: target });\n"+
		"    xhr.open(\"GET\", path, true);\n"+
		"    xhr.send();\n"+
		"}\n"+
		"\n"+
		"function camliGetPermanodeClaims(permanode, opts) {\n"+
		"    var xhr = camliJsonXhr(\"camliGetPermanodeClaims\", opts);\n"+
		"    var path = Camli.config.searchRoot + \"camli/search/claims?permanode=\" +\n"+
		"        permanode;\n"+
		"    xhr.open(\"GET\", path, true);\n"+
		"    xhr.send();\n"+
		"}\n"+
		"\n"+
		"function camliGetBlobContents(blobref, opts) {\n"+
		"    opts = Camli.saneOpts(opts);\n"+
		"    var xhr = new XMLHttpRequest();\n"+
		"    xhr.onreadystatechange = function() {\n"+
		"        if (xhr.readyState != 4) { return; }\n"+
		"        if (xhr.status != 200) {\n"+
		"            opts.fail(\"camliGetBlobContents HTTP status \" + xhr.status);\n"+
		"            return;\n"+
		"        }\n"+
		"        opts.success(xhr.responseText);\n"+
		"    };\n"+
		"    xhr.open(\"GET\", camliBlobURL(blobref), true);\n"+
		"    xhr.send();\n"+
		"}\n"+
		"\n"+
		"function camliBlobURL(blobref) {\n"+
		"    return Camli.config.blobRoot + \"camli/\" + blobref;\n"+
		"}\n"+
		"\n"+
		"function camliDescribeBlogURL(blobref) {\n"+
		"    return Camli.config.searchRoot + 'camli/search/describe?blobref=' + blobref;\n"+
		"}\n"+
		"\n"+
		"function camliSign(clearObj, opts) {\n"+
		"    opts = Camli.saneOpts(opts);\n"+
		"    var sigConf = Camli.config.signing;\n"+
		"    if (!sigConf || !sigConf.publicKeyBlobRef) {\n"+
		"       camliCondCall(opts.fail, \"Missing Camli.config.signing.publicKeyBlobRef\");\n"+
		"       return;\n"+
		"    }\n"+
		"\n"+
		"    clearObj.camliSigner = sigConf.publicKeyBlobRef;\n"+
		"    clearText = JSON.stringify(clearObj, null, 2);\n"+
		"\n"+
		"    var xhr = new XMLHttpRequest();\n"+
		"    xhr.onreadystatechange = function() {\n"+
		"       if (xhr.readyState != 4) { return; }\n"+
		"       if (xhr.status != 200) {\n"+
		"          opts.fail(\"got status \" + xhr.status);\n"+
		"          return;\n"+
		"       }\n"+
		"       opts.success(xhr.responseText);\n"+
		"    };\n"+
		"    xhr.open(\"POST\", sigConf.signHandler, true);\n"+
		"    xhr.setRequestHeader(\"Content-Type\", \"application/x-www-form-urlencoded\");\n"+
		"    xhr.send(\"json=\" + encodeURIComponent(clearText));\n"+
		"}\n"+
		"\n"+
		"/**\n"+
		" * @param {?} file File object to upload.\n"+
		" * @param {Camli.OptCallbacks} opts callbacks. \n"+
		" *\n"+
		"// camliUploadFile uploads a file and returns a file schema. It does not create\n"+
		"// any permanodes.\n"+
		"//\n"+
		"// file: File object\n"+
		"// opts: optional callbacks:\n"+
		"// opts:\n"+
		"//   - fail: function(msg)\n"+
		"//   - success: function(fileBlobRef) of the server-validated or\n"+
		"//         just-uploaded file schema blob.\n"+
		"//   - onContentsRef: function(blobref) of contents, once hashed in-browser\n"+
		"*/\n"+
		"function camliUploadFile(file, opts) {\n"+
		"    var fr = new FileReader();\n"+
		"    fr.onload = function() {\n"+
		"        var dataurl = fr.result;\n"+
		"        var comma = dataurl.indexOf(\",\");\n"+
		"        if (comma != -1) {\n"+
		"            var b64 = dataurl.substring(comma + 1);\n"+
		"            var arrayBuffer = Base64.decode(b64).buffer;\n"+
		"            var hash = Crypto.SHA1(new Uint8Array(arrayBuffer, 0));\n"+
		"\n"+
		"            var contentsRef = \"sha1-\" + hash;\n"+
		"            camliCondCall(opts.onContentsRef, contentsRef);\n"+
		"            camliUploadFileHelper(file, contentsRef, {\n"+
		"                success: opts.success, fail: opts.fail\n"+
		"            });\n"+
		"        }\n"+
		"    };\n"+
		"    fr.onerror = function() {\n"+
		"        console.log(\"FileReader onerror: \" + fr.error + \" code=\" + fr.error.code)"+
		";\n"+
		"    };\n"+
		"    fr.readAsDataURL(file);\n"+
		"}\n"+
		"\n"+
		"// camliUploadFileHelper uploads the provided file with contents blobref contents"+
		"BlobRef\n"+
		"// and returns a blobref of a file blob.  It does not create any permanodes.\n"+
		"// Most callers will use camliUploadFile instead of this helper.\n"+
		"//\n"+
		"// camliUploadFileHelper only uploads chunks of the file if they don't already ex"+
		"ist\n"+
		"// on the server. It starts by assuming the file might already exist on the serve"+
		"r\n"+
		"// and, if so, uses an existing (but re-verified) file schema ref instead.\n"+
		"//\n"+
		"// file: File object\n"+
		"// contentsBlobRef: blob ref of file as sha1'd locally\n"+
		"// opts:\n"+
		"//   - fail: function(msg)\n"+
		"//   - success: function(fileBlobRef) of the server-validated or\n"+
		"//         just-uploaded file schema blob.\n"+
		"function camliUploadFileHelper(file, contentsBlobRef, opts) {\n"+
		"    opts = Camli.saneOpts(opts);\n"+
		"    if (!Camli.config.uploadHelper) {\n"+
		"        opts.fail(\"no uploadHelper available\");\n"+
		"        return\n"+
		"    }\n"+
		"\n"+
		"    var doUpload = function() {\n"+
		"        var fd = new FormData();\n"+
		"        fd.append(\"TODO-some-uploadHelper-form-name\", file);\n"+
		"        var uploadCb = { fail: opts.fail };\n"+
		"        uploadCb.success = function(res) {\n"+
		"            if (res.got && res.got.length == 1 && res.got[0].fileref) {\n"+
		"                var fileblob = res.got[0].fileref;\n"+
		"                console.log(\"uploaded \" + contentsBlobRef + \" => file blob \" + fi"+
		"leblob);\n"+
		"                opts.success(fileblob);\n"+
		"            } else {\n"+
		"                opts.fail(\"failed to upload \" + file.name + \": \" + contentsBlobRe"+
		"f + \": \" + JSON.stringify(res, null, 2))\n"+
		"            }\n"+
		"        };\n"+
		"        var xhr = camliJsonXhr(\"camliUploadFileHelper\", uploadCb);\n"+
		"        xhr.open(\"POST\", Camli.config.uploadHelper);\n"+
		"        xhr.send(fd);\n"+
		"    };\n"+
		"\n"+
		"    var dupcheckCb = { fail: opts.fail };\n"+
		"    dupcheckCb.success = function(res) {\n"+
		"        var remain = res.files;\n"+
		"        var checkNext;\n"+
		"        checkNext = function() {\n"+
		"            if (remain.length == 0) {\n"+
		"                doUpload();\n"+
		"                return;\n"+
		"            }\n"+
		"            // TODO: verify filename and other file metadata in the\n"+
		"            // file json schema match too, not just the contents\n"+
		"            var checkFile = remain.shift();\n"+
		"            console.log(\"integrity checking the reported dup \" + checkFile);\n"+
		"\n"+
		"            var vcb = {};\n"+
		"            vcb.fail = function(xhr) {\n"+
		"                console.log(\"integrity checked failed on \" + checkFile);\n"+
		"                checkNext();\n"+
		"            };\n"+
		"            vcb.success = function(xhr) {\n"+
		"                if (xhr.getResponseHeader(\"X-Camli-Contents\") == contentsBlobRef)"+
		" {\n"+
		"                    console.log(\"integrity checked passed on \" + checkFile + \"; u"+
		"sing it.\");\n"+
		"                    opts.success(checkFile);\n"+
		"                } else {\n"+
		"                    checkNext();\n"+
		"                }\n"+
		"            };\n"+
		"            var xhr = camliXhr(\"headVerifyFile\", vcb);\n"+
		"            xhr.open(\"HEAD\", Camli.config.downloadHelper + checkFile + \"/?verifyc"+
		"ontents=\" + contentsBlobRef, true);\n"+
		"            xhr.send();\n"+
		"        };\n"+
		"        checkNext();\n"+
		"    };\n"+
		"    camliFindExistingFileSchemas(contentsBlobRef, dupcheckCb);\n"+
		"}\n"+
		"\n"+
		"function camliUploadString(s, opts) {\n"+
		"    opts = Camli.saneOpts(opts);\n"+
		"    var blobref = \"sha1-\" + Crypto.SHA1(s);\n"+
		"    var parts = [s];\n"+
		"\n"+
		"    var bb = new Blob(parts);\n"+
		"\n"+
		"    var fd = new FormData();\n"+
		"    fd.append(blobref, bb);\n"+
		"\n"+
		"    var uploadCb = {};\n"+
		"    uploadCb.success = function(resj) {\n"+
		"        // TODO: check resj.received[] array.\n"+
		"        opts.success(blobref);\n"+
		"    };\n"+
		"    uploadCb.fail = opts.fail;\n"+
		"    var xhr = camliJsonXhr(\"camliUploadString\", uploadCb);\n"+
		"    // TODO: hack, hard-coding the upload URL here.\n"+
		"    // Change the spec now that App Engine permits 32 MB requests\n"+
		"    // and permit a PUT request on the sha1?  Or at least let us\n"+
		"    // specify the well-known upload URL?  In cases like this, uploading\n"+
		"    // a new permanode, it's silly to even stat.\n"+
		"    xhr.open(\"POST\", Camli.config.blobRoot + \"camli/upload\")\n"+
		"    xhr.send(fd);\n"+
		"}\n"+
		"\n"+
		"function camliCreateNewPermanode(opts) {\n"+
		"    opts = Camli.saneOpts(opts);\n"+
		"     var json = {\n"+
		"         \"camliVersion\": 1,\n"+
		"         \"camliType\": \"permanode\",\n"+
		"         \"random\": \"\"+Math.random()\n"+
		"     };\n"+
		"     camliSign(json, {\n"+
		"                   success: function(got) {\n"+
		"                       camliUploadString(\n"+
		"                           got,\n"+
		"                           {\n"+
		"                               success: opts.success,\n"+
		"                               fail: function(msg) {\n"+
		"                                   opts.fail(\"upload permanode fail: \" + msg);\n"+
		"                               }\n"+
		"                           });\n"+
		"                   },\n"+
		"                   fail: function(msg) {\n"+
		"                       opts.fail(\"sign permanode fail: \" + msg);\n"+
		"                   }\n"+
		"               });\n"+
		"}\n"+
		"\n"+
		"// Returns the first value from the query string corresponding to |key|.\n"+
		"// Returns null if the key isn't present.\n"+
		"Camli.getQueryParam = function(key) {\n"+
		"    var params = document.location.search.substring(1).split('&');\n"+
		"    for (var i = 0; i < params.length; ++i) {\n"+
		"        var parts = params[i].split('=');\n"+
		"        if (parts.length == 2 && decodeURIComponent(parts[0]) == key)\n"+
		"            return decodeURIComponent(parts[1]);\n"+
		"    }\n"+
		"    return null;\n"+
		"};\n"+
		"\n"+
		"function camliGetRecentlyUpdatedPermanodes(opts) {\n"+
		"    // opts.thumbnails is the maximum size of the thumbnails we want,\n"+
		"    // or 0 if no thumbnail.\n"+
		"    var path = Camli.config.searchRoot + \"camli/search/recent\";\n"+
		"    if (opts.thumbnails != null) {\n"+
		"        path = Camli.makeURL(path, {thumbnails: opts.thumbnails});\n"+
		"    }\n"+
		"    var xhr = camliJsonXhr(\"camliGetRecentlyUpdatedPermanodes\", opts);\n"+
		"    xhr.open(\"GET\", path, true);\n"+
		"    xhr.send();\n"+
		"}\n"+
		"\n"+
		"function camliGetPermanodesWithAttr(signer, attr, value, fuzzy, max, opts) {\n"+
		"    var xhr = camliJsonXhr(\"camliGetPermanodesWithAttr\", opts);\n"+
		"    var path = Camli.makeURL(Camli.config.searchRoot + \"camli/search/permanodeatt"+
		"r\",\n"+
		"                       { signer: signer, attr: attr, value: value, fuzzy: fuzzy, "+
		"max: max});\n"+
		"    xhr.open(\"GET\", path, true);\n"+
		"    xhr.send();\n"+
		"}\n"+
		"\n"+
		"function camliXhr(name, opts) {\n"+
		"    opts = Camli.saneOpts(opts);\n"+
		"    var xhr = new XMLHttpRequest();\n"+
		"    xhr.onreadystatechange = function() {\n"+
		"        if (xhr.readyState != 4) { return; }\n"+
		"        if (xhr.status == 200) {\n"+
		"            opts.success(xhr);\n"+
		"        } else {\n"+
		"            opts.fail(name + \": expected status 200; got \" + xhr.status + \": \" + "+
		"xhr.responseText);\n"+
		"        }\n"+
		"    };\n"+
		"    return xhr;\n"+
		"}\n"+
		"\n"+
		"function camliJsonXhr(name, opts) {\n"+
		"    opts = Camli.saneOpts(opts);\n"+
		"    var xhr = new XMLHttpRequest();\n"+
		"    xhr.onreadystatechange = function() {\n"+
		"        if (xhr.readyState != 4) { return; }\n"+
		"        if (xhr.status != 200) {\n"+
		"            try {\n"+
		"                var resj = JSON.parse(xhr.responseText);\n"+
		"                opts.fail(name + \": expected status 200; got \" + xhr.status + \": "+
		"\" + resj.error);\n"+
		"            } catch(x) {\n"+
		"                opts.fail(name + \": expected status 200; got \" + xhr.status + \": "+
		"\" + xhr.responseText);\n"+
		"            }\n"+
		"            return;\n"+
		"        }\n"+
		"        var resj;\n"+
		"        try {\n"+
		"            resj = JSON.parse(xhr.responseText);\n"+
		"        } catch(x) {\n"+
		"            opts.fail(name + \": error parsing JSON in response: \" + xhr.responseT"+
		"ext);\n"+
		"            return\n"+
		"        }\n"+
		"        if (resj.error) {\n"+
		"            opts.fail(resj.error);\n"+
		"        } else {\n"+
		"            opts.success(resj);\n"+
		"        }\n"+
		"    };\n"+
		"    return xhr;\n"+
		"}\n"+
		"\n"+
		"function camliFindExistingFileSchemas(wholeDigestRef, opts) {\n"+
		"    var xhr = camliJsonXhr(\"camliFindExistingFileSchemas\", opts);\n"+
		"    var path = Camli.config.searchRoot + \"camli/search/files?wholedigest=\" +\n"+
		"        wholeDigestRef;\n"+
		"    xhr.open(\"GET\", path, true);\n"+
		"    xhr.send();\n"+
		"}\n"+
		"\n"+
		"// Returns true if the passed-in string might be a blobref.\n"+
		"Camli.isPlausibleBlobRef = function(blobRef) {\n"+
		"    return /^\\w+-[a-f0-9]+$/.test(blobRef);\n"+
		"};\n"+
		"\n"+
		"Camli.linkifyBlobRefs = function(schemaBlob) {\n"+
		"    var re = /(\\w{3,6}-[a-f0-9]{30,})/g;\n"+
		"    return schemaBlob.replace(re, \"<a href='./?b=$1'>$1</a>\");\n"+
		"};\n"+
		"\n"+
		"// Helper function for camliNewSetAttributeClaim() (and eventually, for\n"+
		"// similar functions to add or delete attributes).\n"+
		"Camli.changeAttribute = function(permanode, claimType, attribute, value, opts) {\n"+
		"    opts = Camli.saneOpts(opts);\n"+
		"    var json = {\n"+
		"        \"camliVersion\": 1,\n"+
		"        \"camliType\": \"claim\",\n"+
		"        \"permaNode\": permanode,\n"+
		"        \"claimType\": claimType,\n"+
		"        \"claimDate\": Camli.dateToRfc3339String(new Date()),\n"+
		"        \"attribute\": attribute,\n"+
		"        \"value\": value\n"+
		"    };\n"+
		"    camliSign(json, {\n"+
		"        success: function(signedBlob) {\n"+
		"            camliUploadString(signedBlob, {\n"+
		"                success: opts.success,\n"+
		"                fail: function(msg) {\n"+
		"                    opts.fail(\"upload \" + claimType + \" fail: \" + msg);\n"+
		"                }\n"+
		"            });\n"+
		"        },\n"+
		"        fail: function(msg) {\n"+
		"            opts.fail(\"sign \" + claimType + \" fail: \" + msg);\n"+
		"        }\n"+
		"    });\n"+
		"};\n"+
		"\n"+
		"// pn: permanode to find a good title of\n"+
		"// des: a describe \"meta\" map from blobref to DescribedBlob.\n"+
		"function camliBlobTitle(pn, des) {\n"+
		"    var d = des[pn];\n"+
		"    if (!d) {\n"+
		"        return pn;\n"+
		"    }\n"+
		"    if (d.camliType == \"file\" && d.file && d.file.fileName) {\n"+
		"        return d.file.fileName;\n"+
		"    }\n"+
		"    if (d.camliType == \"directory\" && d.dir && d.dir.fileName) {\n"+
		"        return d.dir.fileName;\n"+
		"    }\n"+
		"    if (d.permanode) {\n"+
		"        var attr = d.permanode.attr;\n"+
		"        if (!attr) {\n"+
		"            return pn;\n"+
		"        }\n"+
		"        if (attr.title) {\n"+
		"            return attr.title[0];\n"+
		"        }\n"+
		"        if (attr.camliContent) {\n"+
		"            return camliBlobTitle(attr.camliContent[0], des);\n"+
		"        }\n"+
		"    }\n"+
		"    return pn;\n"+
		"}\n"+
		"\n"+
		"// Create and upload a new set-attribute claim.\n"+
		"function camliNewSetAttributeClaim(permanode, attribute, value, opts) {\n"+
		"    Camli.changeAttribute(permanode, \"set-attribute\", attribute, value, opts);\n"+
		"}\n"+
		"\n"+
		"// Create and upload a new add-attribute claim.\n"+
		"function camliNewAddAttributeClaim(permanode, attribute, value, opts) {\n"+
		"    Camli.changeAttribute(permanode, \"add-attribute\", attribute, value, opts);\n"+
		"}\n"+
		"\n"+
		"// Create and upload a new del-attribute claim.\n"+
		"function camliNewDelAttributeClaim(permanode, attribute, value, opts) {\n"+
		"    Camli.changeAttribute(permanode, \"del-attribute\", attribute, value, opts);\n"+
		"}\n"+
		"\n"+
		"/**\n"+
		" * camliCondCall calls fn, if non-null, with the remaining parameters.\n"+
		" *\n"+
		" * @param {Function?} fn \n"+
		" * @param {...?} var_args\n"+
		" */\n"+
		"function camliCondCall(fn, var_args) {\n"+
		"    if (!fn) {\n"+
		"        return;\n"+
		"    }\n"+
		"    fn.apply(null, Array.prototype.slice.call(arguments, 1));\n"+
		"}\n"+
		""))
}
