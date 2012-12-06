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

function handleFormTitleSubmit(e) {
    e.stopPropagation();
    e.preventDefault();

    var inputTitle = document.getElementById("inputTitle");
    inputTitle.disabled = true;
    var btnSaveTitle = document.getElementById("btnSaveTitle");
    btnSaveTitle.disabled = true;

    var startTime = new Date();

    camliNewSetAttributeClaim(
        getPermanodeParam(),
        "title",
        inputTitle.value,
        {
            success: function() {
                var elapsedMs = new Date().getTime() - startTime.getTime();
                setTimeout(function() {
                    inputTitle.disabled = false;
                    btnSaveTitle.disabled = false;
                    buildPermanodeUi();
                }, Math.max(250 - elapsedMs, 0));
            },
            fail: function(msg) {
                alert(msg);
                inputTitle.disabled = false;
                btnSaveTitle.disabled = false;
            }
        });
}

function handleFormTagsSubmit(e) {
    e.stopPropagation();
    e.preventDefault();

    var input = document.getElementById("inputNewTag");
    var btn = document.getElementById("btnAddTag");

    if (input.value == "") {
        return;
    }

    input.disabled = true;
    btn.disabled = true;

    var startTime = new Date();

    var tags = input.value.split(/\s*,\s*/);
    var nRemain = tags.length;

    var oneDone = function() {
        nRemain--;
        if (nRemain == 0) {
            var elapsedMs = new Date().getTime() - startTime.getTime();
            setTimeout(function() {
                           input.value = '';
                           input.disabled = false;
                           btn.disabled = false;
                           buildPermanodeUi();
                       }, Math.max(250 - elapsedMs, 0));
        }
    };
    for (idx in tags) {
        var tag = tags[idx];
        camliNewAddAttributeClaim(
            getPermanodeParam(),
            "tag",
            tag,
            {
                success: oneDone,
                fail: function(msg) {
                    alert(msg);
                    oneDone();
                }
            });
    }
}

function handleFormAccessSubmit(e) {
    e.stopPropagation();
    e.preventDefault();

    var selectAccess = document.getElementById("selectAccess");
    selectAccess.disabled = true;
    var btnSaveAccess = document.getElementById("btnSaveAccess");
    btnSaveAccess.disabled = true;

    var operation = camliNewDelAttributeClaim;
    var value = "";
    if (selectAccess.value != "private") {
        operation = camliNewSetAttributeClaim;
        value = selectAccess.value;
    }

    var startTime = new Date();

    operation(
        getPermanodeParam(),
        "camliAccess",
        value,
        {
            success: function() {
                var elapsedMs = new Date().getTime() - startTime.getTime();
                setTimeout(function() {
                    selectAccess.disabled = false;
                    btnSaveAccess.disabled = false;
                }, Math.max(250 - elapsedMs, 0));
            },
            fail: function(msg) {
                alert(msg);
                selectAccess.disabled = false;
                btnSaveAccess.disabled = false;
            }
        });
}

function deleteTagFunc(tag, strikeEle, removeEle) {
    return function(e) {
        strikeEle.innerHTML = "<del>" + strikeEle.innerHTML + "</del>";
        camliNewDelAttributeClaim(
            getPermanodeParam(),
            "tag",
            tag,
            {
                success: function() {
                    removeEle.parentNode.removeChild(removeEle);
                },
                fail: function(msg) {
                    alert(msg);
                }
            });
    };
}

function onTypeChange() {
    var sel = document.getElementById("type");
    var dnd = document.getElementById("dnd");
    var btnGallery = document.getElementById("btnGallery");

    if (sel.value == "collection" || sel.value == "") {
        dnd.style.display = "block";
        btnGallery.style.visibility = 'visible';
    } else {
        dnd.style.display = "none";
        btnGallery.style.visibility = 'hidden';
    }
}

var lastFiles;
function handleFiles(files) {
    lastFiles = files;

    for (var i = 0; i < files.length; i++) {
        var file = files[i];
        startFileUpload(file);
    }
}

function startFileUpload(file) {
    var dnd = document.getElementById("dnd");
    var up = document.createElement("div");
    up.className= 'camli-dnd-item';
    dnd.appendChild(up);
    var info = "name=" + file.name + " size=" + file.size + "; type=" + file.type;

    var setStatus = function(status) {
        up.innerHTML = info + " " + status;
    };
    setStatus("(scanning)");

    var contentsRef; // set later

    var onFail = function(msg) {
        up.innerHTML = info + " <strong>fail:</strong> ";
        up.appendChild(document.createTextNode(msg));
    };

    var onGotFileSchemaRef = function(fileref) {
        setStatus(" <strong>fileref: " + fileref + "</strong>");
        camliCreateNewPermanode(
            {
            success: function(filepn) {
                var doneWithAll = function() {
                    setStatus("- done");
                    buildPermanodeUi();
                };
                var addMemberToParent = function() {
                    setStatus("adding member");
                    camliNewAddAttributeClaim(getPermanodeParam(), "camliMember", filepn, { success: doneWithAll, fail: onFail });
                };
                var makePermanode = function() {
                    setStatus("making permanode");
                    camliNewSetAttributeClaim(filepn, "camliContent", fileref, { success: addMemberToParent, fail: onFail });
                };
                makePermanode();
            },
            fail: onFail
        });
    };

    var fr = new FileReader();
    fr.onload = function() {
        dataurl = fr.result;
        comma = dataurl.indexOf(",");
        if (comma != -1) {
            b64 = dataurl.substring(comma + 1);
            var arrayBuffer = Base64.decode(b64).buffer;
            var hash = Crypto.SHA1(new Uint8Array(arrayBuffer, 0));

            contentsRef = "sha1-" + hash;
            setStatus("(checking for dup of " + contentsRef + ")");
            camliUploadFileHelper(file, contentsRef, {
              success: onGotFileSchemaRef, fail: onFail
            });
        }
    };
    fr.onerror = function() {
        console.log("FileReader onerror: " + fr.error + " code=" + fr.error.code);
    };
    fr.readAsDataURL(file);
}

function onFileFormSubmit(e) {
    e.stopPropagation();
    e.preventDefault();
    alert("TODO: upload");
}

function onFileInputChange(e) {
    handleFiles(document.getElementById("fileInput").files);
}

function setupFilesHandlers() {
    var dnd = document.getElementById("dnd");
    document.getElementById("fileForm").addEventListener("submit", onFileFormSubmit);
    document.getElementById("fileInput").addEventListener("change", onFileInputChange);

    var stop = function(e) {
        this.classList && this.classList.add('camli-dnd-over');
        e.stopPropagation();
        e.preventDefault();
    };
    dnd.addEventListener("dragenter", stop, false);
    dnd.addEventListener("dragover", stop, false);


    dnd.addEventListener("dragleave", function() {
            this.classList.remove('camli-dnd-over');
        }, false);

    var drop = function(e) {
        this.classList.remove('camli-dnd-over');
        stop(e);
        var dt = e.dataTransfer;
        var files = dt.files;
        document.getElementById("info").innerHTML = "";
        handleFiles(files);
    };
    dnd.addEventListener("drop", drop, false);
}


// member: child permanode
function deleteMember(member, strikeEle, removeEle) {
  return function(e) {
        strikeEle.innerHTML = "<del>" + strikeEle.innerHTML + "</del>";
        camliNewDelAttributeClaim(
            getPermanodeParam(),
            "camliMember",
            member,
            {
                success: function() {
                    removeEle.parentNode.removeChild(removeEle);
                },
                fail: function(msg) {
                    alert(msg);
                }
            });
    };
}

// pn: child permanode
// des: describe response of root permanode
function addMember(pn, des) {
    var membersDiv = document.getElementById("members");
    var ul;
    if (membersDiv.innerHTML == "") {
        membersDiv.appendChild(document.createTextNode("Members:"));
        ul = document.createElement("ul");
        membersDiv.appendChild(ul);
    } else {
        ul = membersDiv.firstChild.nextSibling;
    }
    var li = document.createElement("li");
    var a = document.createElement("a");
    a.href = "./?p=" + pn;
    setTextContent(a, camliBlobTitle(pn, des));

    var del = document.createElement("span");
    del.className = 'camli-del';
    setTextContent(del, "x");
    del.addEventListener("click", deleteMember(pn, a, li));

    li.appendChild(a);
    li.appendChild(del);
    ul.appendChild(li);
}

function buildPermanodeUi() {
    camliDescribeBlob(getPermanodeParam(), {
        thumbnails: 200, // requested size
        success: onBlobDescribed,
        failure: function(msg) {
            alert("failed to get blob description: " + msg);
        }
    });
}

function onBlobDescribed(jres) {
    var permanode = getPermanodeParam();
    if (!jres[permanode]) {
        alert("didn't get blob " + permanode);
        return;
    }
    var permanodeObject = jres[permanode].permanode;
    if (!permanodeObject) {
        alert("blob " + permanode + " isn't a permanode");
        return;
    }

    setTextContent(document.getElementById("debugattrs"), JSON.stringify(permanodeObject.attr, null, 2));

    var attr = function(name) {
        if (!(name in permanodeObject.attr)) {
            return null;          
        }
        if (permanodeObject.attr[name].length == 0) {
            return null;
        }
        return permanodeObject.attr[name][0];
    };

    var disablePublish = false;

    var selType = document.getElementById("type");
    if (attr("camliRoot")) {
        selType.value = "root";
        disablePublish = true;  // can't give a URL to a root with a claim
    } else if (attr("camliContent")) {
        selType.value = "file";
    } else if (attr("camliMember")) {
        selType.value = "collection";
    }
    onTypeChange();

    document.getElementById("selectPublishRoot").disabled = disablePublish;
    document.getElementById("publishSuffix").disabled = disablePublish;
    document.getElementById("btnSavePublish").disabled = disablePublish;

    var inputTitle = document.getElementById("inputTitle");
    inputTitle.value = attr("title") ? attr("title") : "";
    inputTitle.disabled = false;

    var spanTags = document.getElementById("spanTags");
    while (spanTags.firstChild) {
        spanTags.removeChild(spanTags.firstChild);
    }

    document.getElementById('members').innerHTML = '';
    var members = permanodeObject.attr.camliMember;
    if (members && members.length > 0) {
        for (idx in members) {
            var member = members[idx];
            addMember(member, jres);
        }
    }

    var camliContent = permanodeObject.attr.camliContent;
    if (camliContent && camliContent.length > 0) {
        camliContent = camliContent[camliContent.length-1];
        var c = document.getElementById("content");
        c.innerHTML = "";
        c.appendChild(document.createTextNode("File: "));
        var a = document.createElement("a");
        a.href = "./?b=" + camliContent;
        var doThumb = false;
        var thumbnailSrc = jres[permanode].thumbnailSrc;
        var contentObject = jres[camliContent];
        if (thumbnailSrc && contentObject) {
            var objectFile = contentObject.file;
            if (objectFile) {
                if (objectFile.mimeType.indexOf("image/") == 0) {
                    doThumb = true;
                }
            }
        }
        if (doThumb) {
            var img = document.createElement("img");
            img.src = thumbnailSrc;
            a.appendChild(img);
        } else {
            setTextContent(a, camliBlobTitle(camliContent, jres));
        }
        c.appendChild(a);
    }

    var tags = permanodeObject.attr.tag;
    for (idx in tags) {
        var tag = tags[idx];

        var tagSpan = document.createElement("span");
        tagSpan.className = 'camli-tag-c';
        var tagTextEl = document.createElement("span");
        tagTextEl.className = 'camli-tag-text';
        setTextContent(tagTextEl, tag);
        tagSpan.appendChild(tagTextEl);

        var tagDel = document.createElement("span");
        tagDel.className = 'camli-del';
        setTextContent(tagDel, "x");
        tagDel.addEventListener("click", deleteTagFunc(tag, tagTextEl, tagSpan));

        tagSpan.appendChild(tagDel);
        spanTags.appendChild(tagSpan);
    }

    var selectAccess = document.getElementById("selectAccess");
    var access = permanodeObject.attr.camliAccess;
    selectAccess.value = (access && access.length) ? access[0] : "private";
    selectAccess.disabled = false;

    var btnSaveTitle = document.getElementById("btnSaveTitle");
    btnSaveTitle.disabled = false;

    var btnSaveAccess = document.getElementById("btnSaveAccess");
    btnSaveAccess.disabled = false;
}

function setupRootsDropdown() {
    var selRoots = document.getElementById("selectPublishRoot");
    if (!Camli.config.publishRoots) {
        console.log("no publish roots");
        return;
    }
    for (var rootName in Camli.config.publishRoots) {
        var opt = document.createElement("option");
        opt.setAttribute("value", rootName);
        opt.appendChild(document.createTextNode(Camli.config.publishRoots[rootName].prefix[0]));
        selRoots.appendChild(opt);
    }
    document.getElementById("btnSavePublish").addEventListener("click", onBtnSavePublish);
}

function onBtnSavePublish(e) {
    var selRoots = document.getElementById("selectPublishRoot");
    var suffix = document.getElementById("publishSuffix");

    var ourPermanode = getPermanodeParam();
    if (!ourPermanode) {
        return;
    }

    var publishRoot = selRoots.value;
    if (!publishRoot) {
        alert("no publish root selected");
        return;
    } 
    var pathSuffix = suffix.value;
    if (!pathSuffix) {
        alert("no path suffix specified");
        return;
    }

    selRoots.disabled = true;
    suffix.disabled = true;

    var enabled = function() {
        selRoots.disabled = false;
        suffix.disabled = false;
    };

    // Step 1: resolve selRoots.value -> blobref of the root's permanode.
    // Step 2: set attribute on the root's permanode, or a sub-permanode
    // if multiple path components in suffix:
    //         "camliPath:<suffix>" => permanode-of-ourselves

    var sigcb = {};
    sigcb.success = function(sigconf) {
        var savcb = {};
        savcb.success = function(pnres) {
            if (!pnres.permanode) {
                alert("failed to publish root's permanode");
                enabled();
                return;
            }
            var attrcb = {};
            attrcb.success = function() {
                console.log("success.");
                enabled();
                selRoots.value = "";
                suffix.value = "";
                buildPathsList();
            };
            attrcb.fail = function() {
                alert("failed to set attribute");
                enabled();
            };
            camliNewSetAttributeClaim(pnres.permanode, "camliPath:" + pathSuffix, ourPermanode, attrcb);
        };
        savcb.fail = function() {
            alert("failed to find publish root's permanode");
            enabled();
        };
        camliPermanodeOfSignerAttrValue(sigconf.publicKeyBlobRef, "camliRoot", publishRoot, savcb);
    };
    sigcb.fail = function() {
        alert("sig disco failed");
        enabled();
    }
    camliSigDiscovery(sigcb);
}

function buildPathsList() {
    var ourPermanode = getPermanodeParam();
    if (!ourPermanode) {
        return;
    }
    var sigcb = {};
    sigcb.success = function(sigconf) {
        var findpathcb = {};
        findpathcb.success = function(jres) {
            var div = document.getElementById("existingPaths");

            // TODO: there can be multiple paths in this list, but the HTML
            // UI only shows one.  The UI should show all, and when adding a new one
            // prompt users whether they want to add to or replace the existing one.
            // For now we just update the UI to show one.
            // alert(JSON.stringify(jres, null, 2));
            if (jres.paths && jres.paths.length > 0) {
                div.innerHTML = "Existing paths for this permanode:";
                var ul = document.createElement("ul");
                div.appendChild(ul);
                for (var idx in jres.paths) {
                    var path = jres.paths[idx];
                    var li = document.createElement("li");
                    var span = document.createElement("span");
                    li.appendChild(span);

                    var blobLink = document.createElement("a");
                    blobLink.href = ".?p=" + path.baseRef;
                    setTextContent(blobLink, path.baseRef);
                    span.appendChild(blobLink);

                    span.appendChild(document.createTextNode(" - "));

                    var pathLink = document.createElement("a");
                    pathLink.href = "";
                    setTextContent(pathLink, path.suffix);
                    for (var key in Camli.config.publishRoots) {
                        var root = Camli.config.publishRoots[key];
                        if (root.currentPermanode == path.baseRef) {
                            // Prefix should include a trailing slash.
                            pathLink.href = root.prefix[0] + path.suffix;
                            // TODO: Check if we're the latest permanode
                            // for this path and display some "old" notice
                            // if not.
                            break;
                        }
                    }
                    span.appendChild(pathLink);

                    var del = document.createElement("span");
                    del.className = "camli-del";
                    setTextContent(del, "x");
                    del.addEventListener("click", deletePathFunc(path.baseRef, path.suffix, span));
                    span.appendChild(del);

                    ul.appendChild(li);
                }
            } else {
                div.innerHTML = "";
            }
        };
        camliPathsOfSignerTarget(sigconf.publicKeyBlobRef, ourPermanode, findpathcb);
    };
    sigcb.fail = function() {
        alert("sig disco failed");
    }
    camliSigDiscovery(sigcb);
}

function deletePathFunc(sourcePermanode, path, strikeEle) {
    return function(e) {
        strikeEle.innerHTML = "<del>" + strikeEle.innerHTML + "</del>";
        camliNewDelAttributeClaim(
            sourcePermanode,
            "camliPath:" + path,
            getPermanodeParam(),
            {
                success: function() {
                    buildPathsList();
                },
                fail: function(msg) {
                    alert(msg);
                }
            });
    };
}

function btnGoToGallery(e) {
    var permanode = getPermanodeParam();
    if (permanode) {
        window.open('./?g=' + permanode, 'Gallery')
    }
}

function permanodePageOnLoad() {
    var permanode = getPermanodeParam();
    if (permanode) {
        document.getElementById('permanode').innerHTML = "<a href='./?p=" + permanode + "'>" + permanode + "</a>";
        document.getElementById('permanodeBlob').innerHTML = "<a href='./?b=" + permanode + "'>view blob</a>";
    }

    var formTitle = document.getElementById("formTitle");
    formTitle.addEventListener("submit", handleFormTitleSubmit);
    var formTags = document.getElementById("formTags");
    formTags.addEventListener("submit", handleFormTagsSubmit);
    var formAccess = document.getElementById("formAccess");
    formAccess.addEventListener("submit", handleFormAccessSubmit);

    var selectType = document.getElementById("type");
    selectType.addEventListener("change", onTypeChange);
    var btnGallery = document.getElementById("btnGallery");
    btnGallery.addEventListener("click", btnGoToGallery);

    setupRootsDropdown();
    setupFilesHandlers();

    buildPermanodeUi();
    buildPathsList();
}

window.addEventListener("load", permanodePageOnLoad);
