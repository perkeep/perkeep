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

//TODO(mpl): add navigation to dot dot
//TODO(mpl): keep currently displayed tree with identation for children instead of reloading a new tree with a new root?
function onChildrenFound(div, jres) {
//	var div = document.getElementById("children");
	div.innerHTML = "";
	for (var i = 0; i < jres.child.length; i++) {
		var child = jres.child;
		var pdiv = document.createElement("li");
		var alink = document.createElement("a");
		switch (child[i].type) {
		case 'directory':
			alink.href = "./?d=" + child[i].blobref;
			break;
		case 'file':
			alink.href = "./?b=" + child[i].blobref;
			break;
		default:
			alert("not a file or dir");
			break;
		}
		alink.innerText = child[i].name;
		pdiv.appendChild(alink);
		div.appendChild(pdiv);
	}
}

function buildTree() {
	var permanode = getPermanodeParam();

	var div = document.getElementById("children");
	var gftcb = {};
	gftcb.success = function(jres) { onChildrenFound(div, jres); }
	gftcb.fail = function() { alert("fail"); }
	getFileTree(permanode, gftcb)
}

function treePageOnLoad(e) {
	var permanode = getPermanodeParam();
	if (permanode) {
		document.getElementById('permanodeBlob').innerHTML = "<a href='./?b=" + permanode + "'>" + permanode + "</a>";
	}
	buildTree();
}

window.addEventListener("load", treePageOnLoad);
