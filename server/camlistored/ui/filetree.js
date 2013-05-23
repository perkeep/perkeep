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
	var blobRef = Camli.getQueryParam('d');
	return (blobRef && Camli.isPlausibleBlobRef(blobRef)) ? blobRef : null;
}

function newPermWithContent(content) {
	return function(e) {
		var cnpcb = {};
		cnpcb.success = function(permanode) {
			var naaccb = {};
			naaccb.success = function() {
				alert("permanode created");
			}
			naaccb.fail = function(msg) {
//TODO(mpl): remove newly created permanode then?
				alert("set permanode content failed: " + msg);
			}
			camliNewAddAttributeClaim(permanode, "camliContent", content, naaccb);
		}
		cnpcb.fail = function(msg) {
			alert("create permanode failed: " + msg);
		}
	    camliCreateNewPermanode(cnpcb);
	}
}

function getFileTree(blobref, opts) {
	var xhr = camliJsonXhr("getFileTree", opts);
	var path = "./tree/" + blobref
	xhr.open("GET", path, true);
	xhr.send();
}

function insertAfter( referenceNode, newNode )
{
	// nextSibling X2 because of the "P" span
	referenceNode.parentNode.insertBefore( newNode, referenceNode.nextSibling.nextSibling );
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
	// nextSibling X2 because of the "P" span
	node.parentNode.removeChild(node.nextSibling.nextSibling);
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
		alink.id = children[i].blobRef;
		switch (children[i].type) {
		case 'directory':
			Camli.setTextContent(alink, "+ " + children[i].name);
			alink.href = "./?d=" + alink.id;
			alink.onclick = Function("unFold('" + alink.id + "', " + depth + "); return false;");
			break;
		case 'file':
			Camli.setTextContent(alink, "  " + children[i].name);
			alink.href = "./?b=" + alink.id;
			break;
		default:
			alert("not a file or dir");
			break;
		}
		var newPerm = document.createElement("span");
		newPerm.className = "camli-newp";
		Camli.setTextContent(newPerm, "P");
		newPerm.addEventListener("click", newPermWithContent(alink.id));
		pdiv.appendChild(alink);
		pdiv.appendChild(newPerm);
		div.appendChild(pdiv);
	}
}

function buildTree() {
	var blobref = getPermanodeParam();

	var div = document.getElementById("children");
	var gftcb = {};
	gftcb.success = function(jres) { onChildrenFound(div, 0, jres); }
	gftcb.fail = function() { alert("fail"); }
	getFileTree(blobref, gftcb)
}

function treePageOnLoad(e) {
	var blobref = getPermanodeParam();
	if (blobref) {
		var dbcb = {};
		dbcb.success = function(bmap) {
			var binfo = bmap.meta[blobref];
			if (!binfo) {
				alert("Error describing blob " + blobref);
				return;
			}
			if (binfo.camliType != "directory") {
				alert("Does not contain a directory");
				return;
			}
			var gbccb = {};
			gbccb.success = function(data) {
				try {
					finfo = JSON.parse(data);
					var fileName = finfo.fileName;
					var curDir = document.getElementById('curDir');
					curDir.innerHTML = "<a href='./?b=" + blobref + "'>" + fileName + "</a>";
					CamliFileTree.indentStep = 20;
					buildTree();
				} catch(x) {
					alert(x);
					return;
				}
			}
			gbccb.fail = function() {
				alert("failed to get blobcontents");
			}
			camliGetBlobContents(blobref, gbccb);
		}
		dbcb.fail = function(msg) {
			alert("Error describing blob " + blobref + ": " + msg);
		}
		camliDescribeBlob(blobref, dbcb);
	}
}

window.addEventListener("load", treePageOnLoad);
