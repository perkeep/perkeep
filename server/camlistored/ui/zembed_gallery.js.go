// THIS FILE IS AUTO-GENERATED FROM gallery.js
// DO NOT EDIT.

package ui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("gallery.js", 3347, time.Unix(0, 1358726342000000000), fileembed.String("/*\n"+
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
		"// Gets the |g| query parameter, assuming that it looks like a blobref.\n"+
		"\n"+
		"function getPermanodeParam() {\n"+
		"    var blobRef = Camli.getQueryParam('g');\n"+
		"    return (blobRef && Camli.isPlausibleBlobRef(blobRef)) ? blobRef : null;\n"+
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
		"    var br = des[pn];\n"+
		"    var img = document.createElement(\"img\");\n"+
		"    img.src = br.thumbnailSrc;\n"+
		"    img.height = br.thumbnailHeight;\n"+
		"    img.width =  br.thumbnailWidth;\n"+
		"    a.appendChild(img);\n"+
		"    li.appendChild(a);\n"+
		"    var title = document.createElement(\"p\");\n"+
		"    Camli.setTextContent(title, camliBlobTitle(br.blobRef, des));\n"+
		"    title.className = 'camli-ui-thumbtitle';\n"+
		"    li.appendChild(title);\n"+
		"    li.className = 'camli-ui-thumb';\n"+
		"    ul.appendChild(li);\n"+
		"}\n"+
		"\n"+
		"function onBlobDescribed(jres) {\n"+
		"    var permanode = getPermanodeParam();\n"+
		"    if (!jres[permanode]) {\n"+
		"        alert(\"didn't get blob \" + permanode);\n"+
		"        return;\n"+
		"    }\n"+
		"    var permanodeObject = jres[permanode].permanode;\n"+
		"    if (!permanodeObject) {\n"+
		"        alert(\"blob \" + permanode + \" isn't a permanode\");\n"+
		"        return;\n"+
		"    }\n"+
		"\n"+
		"    document.getElementById('members').innerHTML = '';\n"+
		"    var members = permanodeObject.attr.camliMember;\n"+
		"    if (members && members.length > 0) {\n"+
		"        for (idx in members) {\n"+
		"            var member = members[idx];\n"+
		"            camliDescribeBlob(\n"+
		"                member,\n"+
		"                {\n"+
		"                    success: addMember(member, jres),\n"+
		"                    fail: function(msg) {\n"+
		"                        alert(\"Error describing blob \" + blobref + \": \" + msg);\n"+
		"                    }\n"+
		"                }\n"+
		"            );            \n"+
		"            \n"+
		"        }\n"+
		"    }\n"+
		"}\n"+
		"\n"+
		"function buildGallery() {\n"+
		"    camliDescribeBlob(getPermanodeParam(), {\n"+
		"        thumbnails: 100, // requested size\n"+
		"        success: onBlobDescribed,\n"+
		"        failure: function(msg) {\n"+
		"            alert(\"failed to get blob description: \" + msg);\n"+
		"        }\n"+
		"    });\n"+
		"}\n"+
		"\n"+
		"function galleryPageOnLoad(e) {\n"+
		"    var permanode = getPermanodeParam();\n"+
		"    if (permanode) {\n"+
		"        document.getElementById('permanode').innerHTML = \"<a href='./?p=\" + perma"+
		"node + \"'>\" + permanode + \"</a>\";\n"+
		"        document.getElementById('permanodeBlob').innerHTML = \"<a href='./?b=\" + p"+
		"ermanode + \"'>view blob</a>\";\n"+
		"    }\n"+
		"\n"+
		"    buildGallery();\n"+
		"}\n"+
		"\n"+
		"window.addEventListener(\"load\", galleryPageOnLoad);\n"+
		""))
}
