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

// CamliFileTree namespace
var CamliFileTree = {};

// Gets the |d| query parameter, assuming that it looks like a blobref.

function getPermanodeParam() {
	var blobRef = getQueryParam('d');
	return (blobRef && isPlausibleBlobRef(blobRef)) ? blobRef : null;
}

function getFileTree(blobref, opts) {
	var xhr = camliJsonXhr("getFileTree", opts);
	var path = "./tree/" + blobref
	xhr.open("GET", path, true);
	xhr.send();
}

function insertAfter( referenceNode, newNode )
{
	referenceNode.parentNode.insertBefore( newNode, referenceNode.nextSibling );
}

function unFold(blobref, depth) {
	var node = document.getElementById(blobref);
	var div = document.createElement("div");
	var gftcb = {};
	gftcb.success = function(jres) {
		onChildrenFound(div, depth+1, jres);
		insertAfter(node, div)
		node.onclick = Function("fold('" + blobref + "' , " + depth + "); return false;");
	}
	gftcb.fail = function() { alert("fail"); }
	getFileTree(blobref, gftcb);
}

function fold(nodeid, depth) {
	var node = document.getElementById(nodeid);
	node.parentNode.removeChild(node.nextSibling);
	node.onclick = Function("unFold('" + nodeid + "' , " + depth + "); return false;");
}

function onChildrenFound(div, depth, jres) {
	var indent = depth * CamliFileTree.indentStep
	div.innerHTML = "";
	for (var i = 0; i < jres.children.length; i++) {
		var children = jres.children;
		var pdiv = document.createElement("div");
		var alink = document.createElement("a");
		alink.style.paddingLeft=indent + "px"
		switch (children[i].type) {
		case 'directory':
			alink.innerText = "+ " + children[i].name;
			alink.id = children[i].blobRef;
			alink.href = "./?d=" + alink.id;
			alink.onclick = Function("unFold('" + alink.id + "', " + depth + "); return false;");
			break;
		case 'file':
			alink.innerText = "  " + children[i].name;
			alink.href = "./?b=" + children[i].blobRef;
			break;
		default:
			alert("not a file or dir");
			break;
		}
		pdiv.appendChild(alink);
		div.appendChild(pdiv);
	}
}

function buildTree() {
	var permanode = getPermanodeParam();

	var div = document.getElementById("children");
	var gftcb = {};
	gftcb.success = function(jres) { onChildrenFound(div, 0, jres); }
	gftcb.fail = function() { alert("fail"); }
	getFileTree(permanode, gftcb)
}

function treePageOnLoad(e) {
	var permanode = getPermanodeParam();
	if (permanode) {
		document.getElementById('permanodeBlob').innerHTML = "<a href='./?b=" + permanode + "'>" + permanode + "</a>";
	}
	CamliFileTree.indentStep = 20
	buildTree();
}

window.addEventListener("load", treePageOnLoad);
