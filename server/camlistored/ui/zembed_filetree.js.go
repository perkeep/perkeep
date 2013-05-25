// THIS FILE IS AUTO-GENERATED FROM filetree.js
// DO NOT EDIT.

package ui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("filetree.js", 4738, fileembed.String("/*\n"+
		"Copyright 2011 Google Inc.\n"+
		"\n"+
		"Licensed under the Apache License, Version 2.0 (the \"License\");\n"+
		"you may not use this file except in compliance with the License.\n"+
		"You may obtain a copy of the License at\n"+
		"\n"+
		"	 http://www.apache.org/licenses/LICENSE-2.0\n"+
		"\n"+
		"Unless required by applicable law or agreed to in writing, software\n"+
		"distributed under the License is distributed on an \"AS IS\" BASIS,\n"+
		"WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.\n"+
		"See the License for the specific language governing permissions and\n"+
		"limitations under the License.\n"+
		"*/\n"+
		"\n"+
		"// CamliFileTree namespace\n"+
		"var CamliFileTree = {};\n"+
		"\n"+
		"// Gets the |d| query parameter, assuming that it looks like a blobref.\n"+
		"\n"+
		"function getPermanodeParam() {\n"+
		"	var blobRef = Camli.getQueryParam('d');\n"+
		"	return (blobRef && Camli.isPlausibleBlobRef(blobRef)) ? blobRef : null;\n"+
		"}\n"+
		"\n"+
		"function newPermWithContent(content) {\n"+
		"	return function(e) {\n"+
		"		var cnpcb = {};\n"+
		"		cnpcb.success = function(permanode) {\n"+
		"			var naaccb = {};\n"+
		"			naaccb.success = function() {\n"+
		"				alert(\"permanode created\");\n"+
		"			}\n"+
		"			naaccb.fail = function(msg) {\n"+
		"//TODO(mpl): remove newly created permanode then?\n"+
		"				alert(\"set permanode content failed: \" + msg);\n"+
		"			}\n"+
		"			camliNewAddAttributeClaim(permanode, \"camliContent\", content, naaccb);\n"+
		"		}\n"+
		"		cnpcb.fail = function(msg) {\n"+
		"			alert(\"create permanode failed: \" + msg);\n"+
		"		}\n"+
		"	    camliCreateNewPermanode(cnpcb);\n"+
		"	}\n"+
		"}\n"+
		"\n"+
		"function getFileTree(blobref, opts) {\n"+
		"	var xhr = camliJsonXhr(\"getFileTree\", opts);\n"+
		"	var path = \"./tree/\" + blobref\n"+
		"	xhr.open(\"GET\", path, true);\n"+
		"	xhr.send();\n"+
		"}\n"+
		"\n"+
		"function insertAfter( referenceNode, newNode )\n"+
		"{\n"+
		"	// nextSibling X2 because of the \"P\" span\n"+
		"	referenceNode.parentNode.insertBefore( newNode, referenceNode.nextSibling.nextSi"+
		"bling );\n"+
		"}\n"+
		"\n"+
		"function unFold(blobref, depth) {\n"+
		"	var node = document.getElementById(blobref);\n"+
		"	var div = document.createElement(\"div\");\n"+
		"	var gftcb = {};\n"+
		"	gftcb.success = function(jres) {\n"+
		"		onChildrenFound(div, depth+1, jres);\n"+
		"		insertAfter(node, div)\n"+
		"		node.onclick = Function(\"fold('\" + blobref + \"' , \" + depth + \"); return false;"+
		"\");\n"+
		"	}\n"+
		"	gftcb.fail = function() { alert(\"fail\"); }\n"+
		"	getFileTree(blobref, gftcb);\n"+
		"}\n"+
		"\n"+
		"function fold(nodeid, depth) {\n"+
		"	var node = document.getElementById(nodeid);\n"+
		"	// nextSibling X2 because of the \"P\" span\n"+
		"	node.parentNode.removeChild(node.nextSibling.nextSibling);\n"+
		"	node.onclick = Function(\"unFold('\" + nodeid + \"' , \" + depth + \"); return false;"+
		"\");\n"+
		"}\n"+
		"\n"+
		"function onChildrenFound(div, depth, jres) {\n"+
		"	var indent = depth * CamliFileTree.indentStep\n"+
		"	div.innerHTML = \"\";\n"+
		"	for (var i = 0; i < jres.children.length; i++) {\n"+
		"		var children = jres.children;\n"+
		"		var pdiv = document.createElement(\"div\");\n"+
		"		var alink = document.createElement(\"a\");\n"+
		"		alink.style.paddingLeft=indent + \"px\"\n"+
		"		alink.id = children[i].blobRef;\n"+
		"		switch (children[i].type) {\n"+
		"		case 'directory':\n"+
		"			Camli.setTextContent(alink, \"+ \" + children[i].name);\n"+
		"			alink.href = \"./?d=\" + alink.id;\n"+
		"			alink.onclick = Function(\"unFold('\" + alink.id + \"', \" + depth + \"); return fa"+
		"lse;\");\n"+
		"			break;\n"+
		"		case 'file':\n"+
		"			Camli.setTextContent(alink, \"  \" + children[i].name);\n"+
		"			alink.href = \"./?b=\" + alink.id;\n"+
		"			break;\n"+
		"		default:\n"+
		"			alert(\"not a file or dir\");\n"+
		"			break;\n"+
		"		}\n"+
		"		var newPerm = document.createElement(\"span\");\n"+
		"		newPerm.className = \"camli-newp\";\n"+
		"		Camli.setTextContent(newPerm, \"P\");\n"+
		"		newPerm.addEventListener(\"click\", newPermWithContent(alink.id));\n"+
		"		pdiv.appendChild(alink);\n"+
		"		pdiv.appendChild(newPerm);\n"+
		"		div.appendChild(pdiv);\n"+
		"	}\n"+
		"}\n"+
		"\n"+
		"function buildTree() {\n"+
		"	var blobref = getPermanodeParam();\n"+
		"\n"+
		"	var div = document.getElementById(\"children\");\n"+
		"	var gftcb = {};\n"+
		"	gftcb.success = function(jres) { onChildrenFound(div, 0, jres); }\n"+
		"	gftcb.fail = function() { alert(\"fail\"); }\n"+
		"	getFileTree(blobref, gftcb)\n"+
		"}\n"+
		"\n"+
		"function treePageOnLoad(e) {\n"+
		"	var blobref = getPermanodeParam();\n"+
		"	if (blobref) {\n"+
		"		var dbcb = {};\n"+
		"		dbcb.success = function(bmap) {\n"+
		"			var binfo = bmap.meta[blobref];\n"+
		"			if (!binfo) {\n"+
		"				alert(\"Error describing blob \" + blobref);\n"+
		"				return;\n"+
		"			}\n"+
		"			if (binfo.camliType != \"directory\") {\n"+
		"				alert(\"Does not contain a directory\");\n"+
		"				return;\n"+
		"			}\n"+
		"			var gbccb = {};\n"+
		"			gbccb.success = function(data) {\n"+
		"				try {\n"+
		"					finfo = JSON.parse(data);\n"+
		"					var fileName = finfo.fileName;\n"+
		"					var curDir = document.getElementById('curDir');\n"+
		"					curDir.innerHTML = \"<a href='./?b=\" + blobref + \"'>\" + fileName + \"</a>\";\n"+
		"					CamliFileTree.indentStep = 20;\n"+
		"					buildTree();\n"+
		"				} catch(x) {\n"+
		"					alert(x);\n"+
		"					return;\n"+
		"				}\n"+
		"			}\n"+
		"			gbccb.fail = function() {\n"+
		"				alert(\"failed to get blobcontents\");\n"+
		"			}\n"+
		"			camliGetBlobContents(blobref, gbccb);\n"+
		"		}\n"+
		"		dbcb.fail = function(msg) {\n"+
		"			alert(\"Error describing blob \" + blobref + \": \" + msg);\n"+
		"		}\n"+
		"		camliDescribeBlob(blobref, dbcb);\n"+
		"	}\n"+
		"}\n"+
		"\n"+
		"window.addEventListener(\"load\", treePageOnLoad);\n"+
		""), time.Unix(0, 1369518799000000000))
}
