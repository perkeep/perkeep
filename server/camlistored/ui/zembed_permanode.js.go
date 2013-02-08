// THIS FILE IS AUTO-GENERATED FROM permanode.js
// DO NOT EDIT.
package ui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("permanode.js", 20499, fileembed.String("/*\n"+
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
		"// Gets the |p| query parameter, assuming that it looks like a blobref.\n"+
		"\n"+
		"function getPermanodeParam() {\n"+
		"    var blobRef = Camli.getQueryParam('p');\n"+
		"    return (blobRef && Camli.isPlausibleBlobRef(blobRef)) ? blobRef : null;\n"+
		"}\n"+
		"\n"+
		"function handleFormTitleSubmit(e) {\n"+
		"    e.stopPropagation();\n"+
		"    e.preventDefault();\n"+
		"\n"+
		"    var inputTitle = document.getElementById(\"inputTitle\");\n"+
		"    inputTitle.disabled = true;\n"+
		"    var btnSaveTitle = document.getElementById(\"btnSaveTitle\");\n"+
		"    btnSaveTitle.disabled = true;\n"+
		"\n"+
		"    var startTime = new Date();\n"+
		"\n"+
		"    camliNewSetAttributeClaim(\n"+
		"        getPermanodeParam(),\n"+
		"        \"title\",\n"+
		"        inputTitle.value,\n"+
		"        {\n"+
		"            success: function() {\n"+
		"                var elapsedMs = new Date().getTime() - startTime.getTime();\n"+
		"                setTimeout(function() {\n"+
		"                    inputTitle.disabled = false;\n"+
		"                    btnSaveTitle.disabled = false;\n"+
		"                    buildPermanodeUi();\n"+
		"                }, Math.max(250 - elapsedMs, 0));\n"+
		"            },\n"+
		"            fail: function(msg) {\n"+
		"                alert(msg);\n"+
		"                inputTitle.disabled = false;\n"+
		"                btnSaveTitle.disabled = false;\n"+
		"            }\n"+
		"        });\n"+
		"}\n"+
		"\n"+
		"function handleFormTagsSubmit(e) {\n"+
		"    e.stopPropagation();\n"+
		"    e.preventDefault();\n"+
		"\n"+
		"    var input = document.getElementById(\"inputNewTag\");\n"+
		"    var btn = document.getElementById(\"btnAddTag\");\n"+
		"\n"+
		"    if (input.value == \"\") {\n"+
		"        return;\n"+
		"    }\n"+
		"\n"+
		"    input.disabled = true;\n"+
		"    btn.disabled = true;\n"+
		"\n"+
		"    var startTime = new Date();\n"+
		"\n"+
		"    var tags = input.value.split(/\\s*,\\s*/);\n"+
		"    var nRemain = tags.length;\n"+
		"\n"+
		"    var oneDone = function() {\n"+
		"        nRemain--;\n"+
		"        if (nRemain == 0) {\n"+
		"            var elapsedMs = new Date().getTime() - startTime.getTime();\n"+
		"            setTimeout(function() {\n"+
		"                           input.value = '';\n"+
		"                           input.disabled = false;\n"+
		"                           btn.disabled = false;\n"+
		"                           buildPermanodeUi();\n"+
		"                       }, Math.max(250 - elapsedMs, 0));\n"+
		"        }\n"+
		"    };\n"+
		"    for (idx in tags) {\n"+
		"        var tag = tags[idx];\n"+
		"        camliNewAddAttributeClaim(\n"+
		"            getPermanodeParam(),\n"+
		"            \"tag\",\n"+
		"            tag,\n"+
		"            {\n"+
		"                success: oneDone,\n"+
		"                fail: function(msg) {\n"+
		"                    alert(msg);\n"+
		"                    oneDone();\n"+
		"                }\n"+
		"            });\n"+
		"    }\n"+
		"}\n"+
		"\n"+
		"function handleFormAccessSubmit(e) {\n"+
		"    e.stopPropagation();\n"+
		"    e.preventDefault();\n"+
		"\n"+
		"    var selectAccess = document.getElementById(\"selectAccess\");\n"+
		"    selectAccess.disabled = true;\n"+
		"    var btnSaveAccess = document.getElementById(\"btnSaveAccess\");\n"+
		"    btnSaveAccess.disabled = true;\n"+
		"\n"+
		"    var operation = camliNewDelAttributeClaim;\n"+
		"    var value = \"\";\n"+
		"    if (selectAccess.value != \"private\") {\n"+
		"        operation = camliNewSetAttributeClaim;\n"+
		"        value = selectAccess.value;\n"+
		"    }\n"+
		"\n"+
		"    var startTime = new Date();\n"+
		"\n"+
		"    operation(\n"+
		"        getPermanodeParam(),\n"+
		"        \"camliAccess\",\n"+
		"        value,\n"+
		"        {\n"+
		"            success: function() {\n"+
		"                var elapsedMs = new Date().getTime() - startTime.getTime();\n"+
		"                setTimeout(function() {\n"+
		"                    selectAccess.disabled = false;\n"+
		"                    btnSaveAccess.disabled = false;\n"+
		"                }, Math.max(250 - elapsedMs, 0));\n"+
		"            },\n"+
		"            fail: function(msg) {\n"+
		"                alert(msg);\n"+
		"                selectAccess.disabled = false;\n"+
		"                btnSaveAccess.disabled = false;\n"+
		"            }\n"+
		"        });\n"+
		"}\n"+
		"\n"+
		"function deleteTagFunc(tag, strikeEle, removeEle) {\n"+
		"    return function(e) {\n"+
		"        strikeEle.innerHTML = \"<del>\" + strikeEle.innerHTML + \"</del>\";\n"+
		"        camliNewDelAttributeClaim(\n"+
		"            getPermanodeParam(),\n"+
		"            \"tag\",\n"+
		"            tag,\n"+
		"            {\n"+
		"                success: function() {\n"+
		"                    removeEle.parentNode.removeChild(removeEle);\n"+
		"                },\n"+
		"                fail: function(msg) {\n"+
		"                    alert(msg);\n"+
		"                }\n"+
		"            });\n"+
		"    };\n"+
		"}\n"+
		"\n"+
		"function onTypeChange() {\n"+
		"    var sel = document.getElementById(\"type\");\n"+
		"    var dnd = document.getElementById(\"dnd\");\n"+
		"    var btnGallery = document.getElementById(\"btnGallery\");\n"+
		"\n"+
		"    if (sel.value == \"collection\" || sel.value == \"\") {\n"+
		"        dnd.style.display = \"block\";\n"+
		"        btnGallery.style.visibility = 'visible';\n"+
		"    } else {\n"+
		"        dnd.style.display = \"none\";\n"+
		"        btnGallery.style.visibility = 'hidden';\n"+
		"    }\n"+
		"}\n"+
		"\n"+
		"function handleFiles(files) {\n"+
		"    for (var i = 0; i < files.length; i++) {\n"+
		"        var file = files[i];\n"+
		"        startFileUpload(file);\n"+
		"    }\n"+
		"}\n"+
		"\n"+
		"function startFileUpload(file) {\n"+
		"    var dnd = document.getElementById(\"dnd\");\n"+
		"    var up = document.createElement(\"div\");\n"+
		"    up.className= 'camli-dnd-item';\n"+
		"    dnd.appendChild(up);\n"+
		"    var info = \"name=\" + file.name + \" size=\" + file.size + \"; type=\" + file.type"+
		";\n"+
		"\n"+
		"    var setStatus = function(status) {\n"+
		"        up.innerHTML = info + \" \" + status;\n"+
		"    };\n"+
		"    setStatus(\"(scanning)\");\n"+
		"\n"+
		"    var onFail = function(msg) {\n"+
		"        up.innerHTML = info + \" <strong>fail:</strong> \";\n"+
		"        up.appendChild(document.createTextNode(msg));\n"+
		"    };\n"+
		"\n"+
		"    var onGotFileSchemaRef = function(fileref) {\n"+
		"        setStatus(\" <strong>fileref: \" + fileref + \"</strong>\");\n"+
		"        camliCreateNewPermanode(\n"+
		"            {\n"+
		"            success: function(filepn) {\n"+
		"                var doneWithAll = function() {\n"+
		"                    setStatus(\"- done\");\n"+
		"                    buildPermanodeUi();\n"+
		"                };\n"+
		"                var addMemberToParent = function() {\n"+
		"                    setStatus(\"adding member\");\n"+
		"                    camliNewAddAttributeClaim(getPermanodeParam(), \"camliMember\","+
		" filepn, { success: doneWithAll, fail: onFail });\n"+
		"                };\n"+
		"                var makePermanode = function() {\n"+
		"                    setStatus(\"making permanode\");\n"+
		"                    camliNewSetAttributeClaim(filepn, \"camliContent\", fileref, { "+
		"success: addMemberToParent, fail: onFail });\n"+
		"                };\n"+
		"                makePermanode();\n"+
		"            },\n"+
		"            fail: onFail\n"+
		"        });\n"+
		"    };\n"+
		"\n"+
		"    camliUploadFile(file, {\n"+
		"       onContentsRef: function(contentsRef) {\n"+
		"            setStatus(\"(checking for dup of \" + contentsRef + \")\");\n"+
		"       },\n"+
		"       success: onGotFileSchemaRef,\n"+
		"       fail: onFail\n"+
		"    });\n"+
		"}\n"+
		"\n"+
		"function onFileFormSubmit(e) {\n"+
		"    e.stopPropagation();\n"+
		"    e.preventDefault();\n"+
		"    handleFiles(document.getElementById(\"fileInput\").files);\n"+
		"}\n"+
		"\n"+
		"function $(id) { return document.getElementById(id) }\n"+
		"\n"+
		"function onFileInputChange(e) {\n"+
		"    var s = \"\";\n"+
		"    var files = $(\"fileInput\").files;\n"+
		"    for (var i = 0; i < files.length; i++) {\n"+
		"        var file = files[i];\n"+
		"        s += \"<p>\" + file.name + \"</p>\";\n"+
		"    }\n"+
		"    var fl = $(\"filelist\");\n"+
		"    fl.innerHTML = s;\n"+
		"}\n"+
		"\n"+
		"function setupFilesHandlers() {\n"+
		"    var dnd = document.getElementById(\"dnd\");\n"+
		"    document.getElementById(\"fileForm\").addEventListener(\"submit\", onFileFormSubm"+
		"it);\n"+
		"    document.getElementById(\"fileInput\").addEventListener(\"change\", onFileInputCh"+
		"ange);\n"+
		"\n"+
		"    var stop = function(e) {\n"+
		"        this.classList && this.classList.add('camli-dnd-over');\n"+
		"        e.stopPropagation();\n"+
		"        e.preventDefault();\n"+
		"    };\n"+
		"    dnd.addEventListener(\"dragenter\", stop, false);\n"+
		"    dnd.addEventListener(\"dragover\", stop, false);\n"+
		"\n"+
		"\n"+
		"    dnd.addEventListener(\"dragleave\", function() {\n"+
		"            this.classList.remove('camli-dnd-over');\n"+
		"        }, false);\n"+
		"\n"+
		"    var drop = function(e) {\n"+
		"        this.classList.remove('camli-dnd-over');\n"+
		"        stop(e);\n"+
		"        var dt = e.dataTransfer;\n"+
		"        var files = dt.files;\n"+
		"        document.getElementById(\"info\").innerHTML = \"\";\n"+
		"        handleFiles(files);\n"+
		"    };\n"+
		"    dnd.addEventListener(\"drop\", drop, false);\n"+
		"}\n"+
		"\n"+
		"\n"+
		"// member: child permanode\n"+
		"function deleteMember(member, strikeEle, removeEle) {\n"+
		"  return function(e) {\n"+
		"        strikeEle.innerHTML = \"<del>\" + strikeEle.innerHTML + \"</del>\";\n"+
		"        camliNewDelAttributeClaim(\n"+
		"            getPermanodeParam(),\n"+
		"            \"camliMember\",\n"+
		"            member,\n"+
		"            {\n"+
		"                success: function() {\n"+
		"                    removeEle.parentNode.removeChild(removeEle);\n"+
		"                },\n"+
		"                fail: function(msg) {\n"+
		"                    alert(msg);\n"+
		"                }\n"+
		"            });\n"+
		"    };\n"+
		"}\n"+
		"\n"+
		"// pn: child permanode\n"+
		"// des: describe response of root permanode\n"+
		"function addMember(pn, des) {\n"+
		"    var membersDiv = document.getElementById(\"members\");\n"+
		"    var ul;\n"+
		"    if (membersDiv.innerHTML == \"\") {\n"+
		"        membersDiv.appendChild(document.createTextNode(\"Members:\"));\n"+
		"        ul = document.createElement(\"ul\");\n"+
		"        membersDiv.appendChild(ul);\n"+
		"    } else {\n"+
		"        ul = membersDiv.firstChild.nextSibling;\n"+
		"    }\n"+
		"    var li = document.createElement(\"li\");\n"+
		"    var a = document.createElement(\"a\");\n"+
		"    a.href = \"./?p=\" + pn;\n"+
		"    Camli.setTextContent(a, camliBlobTitle(pn, des.meta));\n"+
		"\n"+
		"    var del = document.createElement(\"span\");\n"+
		"    del.className = 'camli-del';\n"+
		"    Camli.setTextContent(del, \"x\");\n"+
		"    del.addEventListener(\"click\", deleteMember(pn, a, li));\n"+
		"\n"+
		"    li.appendChild(a);\n"+
		"    li.appendChild(del);\n"+
		"    ul.appendChild(li);\n"+
		"}\n"+
		"\n"+
		"function buildPermanodeUi() {\n"+
		"    camliDescribeBlob(getPermanodeParam(), {\n"+
		"        thumbnails: 200, // requested size\n"+
		"        success: onBlobDescribed,\n"+
		"        failure: function(msg) {\n"+
		"            alert(\"failed to get blob description: \" + msg);\n"+
		"        }\n"+
		"    });\n"+
		"}\n"+
		"\n"+
		"function onBlobDescribed(jres) {\n"+
		"    var permanode = getPermanodeParam();\n"+
		"    if (!jres.meta[permanode]) {\n"+
		"        alert(\"didn't get blob \" + permanode);\n"+
		"        return;\n"+
		"    }\n"+
		"    var permanodeObject = jres.meta[permanode].permanode;\n"+
		"    if (!permanodeObject) {\n"+
		"        alert(\"blob \" + permanode + \" isn't a permanode\");\n"+
		"        return;\n"+
		"    }\n"+
		"\n"+
		"    Camli.setTextContent(document.getElementById(\"debugattrs\"), JSON.stringify(pe"+
		"rmanodeObject.attr, null, 2));\n"+
		"\n"+
		"    var attr = function(name) {\n"+
		"        if (!(name in permanodeObject.attr)) {\n"+
		"            return null;\n"+
		"        }\n"+
		"        if (permanodeObject.attr[name].length == 0) {\n"+
		"            return null;\n"+
		"        }\n"+
		"        return permanodeObject.attr[name][0];\n"+
		"    };\n"+
		"\n"+
		"    var disablePublish = false;\n"+
		"\n"+
		"    var selType = document.getElementById(\"type\");\n"+
		"    if (attr(\"camliRoot\")) {\n"+
		"        selType.value = \"root\";\n"+
		"        disablePublish = true;  // can't give a URL to a root with a claim\n"+
		"    } else if (attr(\"camliContent\")) {\n"+
		"        selType.value = \"file\";\n"+
		"    } else if (attr(\"camliMember\")) {\n"+
		"        selType.value = \"collection\";\n"+
		"    }\n"+
		"    onTypeChange();\n"+
		"\n"+
		"    document.getElementById(\"selectPublishRoot\").disabled = disablePublish;\n"+
		"    document.getElementById(\"publishSuffix\").disabled = disablePublish;\n"+
		"    document.getElementById(\"btnSavePublish\").disabled = disablePublish;\n"+
		"\n"+
		"    var inputTitle = document.getElementById(\"inputTitle\");\n"+
		"    inputTitle.value = attr(\"title\") ? attr(\"title\") : \"\";\n"+
		"    inputTitle.disabled = false;\n"+
		"\n"+
		"    var spanTags = document.getElementById(\"spanTags\");\n"+
		"    while (spanTags.firstChild) {\n"+
		"        spanTags.removeChild(spanTags.firstChild);\n"+
		"    }\n"+
		"\n"+
		"    document.getElementById('members').innerHTML = '';\n"+
		"    var members = permanodeObject.attr.camliMember;\n"+
		"    if (members && members.length > 0) {\n"+
		"        for (idx in members) {\n"+
		"            var member = members[idx];\n"+
		"            addMember(member, jres);\n"+
		"        }\n"+
		"    }\n"+
		"\n"+
		"    var camliContent = permanodeObject.attr.camliContent;\n"+
		"    if (camliContent && camliContent.length > 0) {\n"+
		"        camliContent = camliContent[camliContent.length-1];\n"+
		"        var c = document.getElementById(\"content\");\n"+
		"        c.innerHTML = \"\";\n"+
		"        var alink = document.createElement(\"a\");\n"+
		"        alink.href = \"./?b=\" + camliContent;\n"+
		"        var img = document.createElement(\"img\");\n"+
		"        var br = jres.meta[permanode];\n"+
		"        img.src = br.thumbnailSrc;\n"+
		"        img.height = br.thumbnailHeight;\n"+
		"        img.width =  br.thumbnailWidth;\n"+
		"        alink.appendChild(img);\n"+
		"        c.appendChild(alink);\n"+
		"        var title = document.createElement(\"p\");\n"+
		"        Camli.setTextContent(title, camliBlobTitle(br.blobRef, jres.meta));\n"+
		"        title.className = 'camli-ui-thumbtitle';\n"+
		"        c.appendChild(title);\n"+
		"    }\n"+
		"\n"+
		"    var tags = permanodeObject.attr.tag;\n"+
		"    for (idx in tags) {\n"+
		"        var tag = tags[idx];\n"+
		"\n"+
		"        var tagSpan = document.createElement(\"span\");\n"+
		"        tagSpan.className = 'camli-tag-c';\n"+
		"        var tagTextEl = document.createElement(\"span\");\n"+
		"        tagTextEl.className = 'camli-tag-text';\n"+
		"        Camli.setTextContent(tagTextEl, tag);\n"+
		"        tagSpan.appendChild(tagTextEl);\n"+
		"\n"+
		"        var tagDel = document.createElement(\"span\");\n"+
		"        tagDel.className = 'camli-del';\n"+
		"        Camli.setTextContent(tagDel, \"x\");\n"+
		"        tagDel.addEventListener(\"click\", deleteTagFunc(tag, tagTextEl, tagSpan));\n"+
		"\n"+
		"        tagSpan.appendChild(tagDel);\n"+
		"        spanTags.appendChild(tagSpan);\n"+
		"    }\n"+
		"\n"+
		"    var selectAccess = document.getElementById(\"selectAccess\");\n"+
		"    var access = permanodeObject.attr.camliAccess;\n"+
		"    selectAccess.value = (access && access.length) ? access[0] : \"private\";\n"+
		"    selectAccess.disabled = false;\n"+
		"\n"+
		"    var btnSaveTitle = document.getElementById(\"btnSaveTitle\");\n"+
		"    btnSaveTitle.disabled = false;\n"+
		"\n"+
		"    var btnSaveAccess = document.getElementById(\"btnSaveAccess\");\n"+
		"    btnSaveAccess.disabled = false;\n"+
		"}\n"+
		"\n"+
		"function setupRootsDropdown() {\n"+
		"    var selRoots = document.getElementById(\"selectPublishRoot\");\n"+
		"    if (!Camli.config.publishRoots) {\n"+
		"        console.log(\"no publish roots\");\n"+
		"        return;\n"+
		"    }\n"+
		"    for (var rootName in Camli.config.publishRoots) {\n"+
		"        var opt = document.createElement(\"option\");\n"+
		"        opt.setAttribute(\"value\", rootName);\n"+
		"        opt.appendChild(document.createTextNode(Camli.config.publishRoots[rootNam"+
		"e].prefix[0]));\n"+
		"        selRoots.appendChild(opt);\n"+
		"    }\n"+
		"    document.getElementById(\"btnSavePublish\").addEventListener(\"click\", onBtnSave"+
		"Publish);\n"+
		"}\n"+
		"\n"+
		"function onBtnSavePublish(e) {\n"+
		"    var selRoots = document.getElementById(\"selectPublishRoot\");\n"+
		"    var suffix = document.getElementById(\"publishSuffix\");\n"+
		"\n"+
		"    var ourPermanode = getPermanodeParam();\n"+
		"    if (!ourPermanode) {\n"+
		"        return;\n"+
		"    }\n"+
		"\n"+
		"    var publishRoot = selRoots.value;\n"+
		"    if (!publishRoot) {\n"+
		"        alert(\"no publish root selected\");\n"+
		"        return;\n"+
		"    }\n"+
		"    var pathSuffix = suffix.value;\n"+
		"    if (!pathSuffix) {\n"+
		"        alert(\"no path suffix specified\");\n"+
		"        return;\n"+
		"    }\n"+
		"\n"+
		"    selRoots.disabled = true;\n"+
		"    suffix.disabled = true;\n"+
		"\n"+
		"    var enabled = function() {\n"+
		"        selRoots.disabled = false;\n"+
		"        suffix.disabled = false;\n"+
		"    };\n"+
		"\n"+
		"    // Step 1: resolve selRoots.value -> blobref of the root's permanode.\n"+
		"    // Step 2: set attribute on the root's permanode, or a sub-permanode\n"+
		"    // if multiple path components in suffix:\n"+
		"    //         \"camliPath:<suffix>\" => permanode-of-ourselves\n"+
		"\n"+
		"    var sigconf = Camli.config.signing;\n"+
		"    var savcb = {};\n"+
		"    savcb.success = function(pnres) {\n"+
		"        if (!pnres.permanode) {\n"+
		"            alert(\"failed to publish root's permanode\");\n"+
		"            enabled();\n"+
		"            return;\n"+
		"        }\n"+
		"        var attrcb = {};\n"+
		"        attrcb.success = function() {\n"+
		"            console.log(\"success.\");\n"+
		"            enabled();\n"+
		"            selRoots.value = \"\";\n"+
		"            suffix.value = \"\";\n"+
		"            buildPathsList();\n"+
		"        };\n"+
		"        attrcb.fail = function() {\n"+
		"            alert(\"failed to set attribute\");\n"+
		"            enabled();\n"+
		"        };\n"+
		"        camliNewSetAttributeClaim(pnres.permanode, \"camliPath:\" + pathSuffix, our"+
		"Permanode, attrcb);\n"+
		"    };\n"+
		"    savcb.fail = function() {\n"+
		"        alert(\"failed to find publish root's permanode\");\n"+
		"        enabled();\n"+
		"    };\n"+
		"    camliPermanodeOfSignerAttrValue(sigconf.publicKeyBlobRef, \"camliRoot\", publis"+
		"hRoot, savcb);\n"+
		"}\n"+
		"\n"+
		"function buildPathsList() {\n"+
		"    var ourPermanode = getPermanodeParam();\n"+
		"    if (!ourPermanode) {\n"+
		"        return;\n"+
		"    }\n"+
		"    var sigconf = Camli.config.signing;\n"+
		"\n"+
		"    var findpathcb = {};\n"+
		"    findpathcb.success = function(jres) {\n"+
		"        var div = document.getElementById(\"existingPaths\");\n"+
		"\n"+
		"        // TODO: there can be multiple paths in this list, but the HTML\n"+
		"        // UI only shows one.  The UI should show all, and when adding a new one\n"+
		"        // prompt users whether they want to add to or replace the existing one.\n"+
		"        // For now we just update the UI to show one.\n"+
		"        // alert(JSON.stringify(jres, null, 2));\n"+
		"        if (jres.paths && jres.paths.length > 0) {\n"+
		"            div.innerHTML = \"Existing paths for this permanode:\";\n"+
		"            var ul = document.createElement(\"ul\");\n"+
		"            div.appendChild(ul);\n"+
		"            for (var idx in jres.paths) {\n"+
		"                var path = jres.paths[idx];\n"+
		"                var li = document.createElement(\"li\");\n"+
		"                var span = document.createElement(\"span\");\n"+
		"                li.appendChild(span);\n"+
		"\n"+
		"                var blobLink = document.createElement(\"a\");\n"+
		"                blobLink.href = \".?p=\" + path.baseRef;\n"+
		"                Camli.setTextContent(blobLink, path.baseRef);\n"+
		"                span.appendChild(blobLink);\n"+
		"\n"+
		"                span.appendChild(document.createTextNode(\" - \"));\n"+
		"\n"+
		"                var pathLink = document.createElement(\"a\");\n"+
		"                pathLink.href = \"\";\n"+
		"                Camli.setTextContent(pathLink, path.suffix);\n"+
		"                for (var key in Camli.config.publishRoots) {\n"+
		"                    var root = Camli.config.publishRoots[key];\n"+
		"                    if (root.currentPermanode == path.baseRef) {\n"+
		"                        // Prefix should include a trailing slash.\n"+
		"                        pathLink.href = root.prefix[0] + path.suffix;\n"+
		"                        // TODO: Check if we're the latest permanode\n"+
		"                        // for this path and display some \"old\" notice\n"+
		"                        // if not.\n"+
		"                        break;\n"+
		"                    }\n"+
		"                }\n"+
		"                span.appendChild(pathLink);\n"+
		"\n"+
		"                var del = document.createElement(\"span\");\n"+
		"                del.className = \"camli-del\";\n"+
		"                Camli.setTextContent(del, \"x\");\n"+
		"                del.addEventListener(\"click\", deletePathFunc(path.baseRef, path.s"+
		"uffix, span));\n"+
		"                span.appendChild(del);\n"+
		"\n"+
		"                ul.appendChild(li);\n"+
		"            }\n"+
		"        } else {\n"+
		"            div.innerHTML = \"\";\n"+
		"        }\n"+
		"    };\n"+
		"    camliPathsOfSignerTarget(sigconf.publicKeyBlobRef, ourPermanode, findpathcb);\n"+
		"}\n"+
		"\n"+
		"function deletePathFunc(sourcePermanode, path, strikeEle) {\n"+
		"    return function(e) {\n"+
		"        strikeEle.innerHTML = \"<del>\" + strikeEle.innerHTML + \"</del>\";\n"+
		"        camliNewDelAttributeClaim(\n"+
		"            sourcePermanode,\n"+
		"            \"camliPath:\" + path,\n"+
		"            getPermanodeParam(),\n"+
		"            {\n"+
		"                success: function() {\n"+
		"                    buildPathsList();\n"+
		"                },\n"+
		"                fail: function(msg) {\n"+
		"                    alert(msg);\n"+
		"                }\n"+
		"            });\n"+
		"    };\n"+
		"}\n"+
		"\n"+
		"function btnGoToGallery(e) {\n"+
		"    var permanode = getPermanodeParam();\n"+
		"    if (permanode) {\n"+
		"        window.open('./?g=' + permanode, 'Gallery')\n"+
		"    }\n"+
		"}\n"+
		"\n"+
		"function permanodePageOnLoad() {\n"+
		"    var permanode = getPermanodeParam();\n"+
		"    if (permanode) {\n"+
		"        document.getElementById('permanode').innerHTML = \"<a href='./?p=\" + perma"+
		"node + \"'>\" + permanode + \"</a>\";\n"+
		"        document.getElementById('permanodeBlob').innerHTML = \"<a href='./?b=\" + p"+
		"ermanode + \"'>view blob</a>\";\n"+
		"    }\n"+
		"\n"+
		"    var formTitle = document.getElementById(\"formTitle\");\n"+
		"    formTitle.addEventListener(\"submit\", handleFormTitleSubmit);\n"+
		"    var formTags = document.getElementById(\"formTags\");\n"+
		"    formTags.addEventListener(\"submit\", handleFormTagsSubmit);\n"+
		"    var formAccess = document.getElementById(\"formAccess\");\n"+
		"    formAccess.addEventListener(\"submit\", handleFormAccessSubmit);\n"+
		"\n"+
		"    var selectType = document.getElementById(\"type\");\n"+
		"    selectType.addEventListener(\"change\", onTypeChange);\n"+
		"    var btnGallery = document.getElementById(\"btnGallery\");\n"+
		"    btnGallery.addEventListener(\"click\", btnGoToGallery);\n"+
		"\n"+
		"    setupRootsDropdown();\n"+
		"    setupFilesHandlers();\n"+
		"\n"+
		"    buildPermanodeUi();\n"+
		"    buildPathsList();\n"+
		"}\n"+
		"\n"+
		"window.addEventListener(\"load\", permanodePageOnLoad);\n"+
		""), time.Unix(0, 1360366137559069951))
}
