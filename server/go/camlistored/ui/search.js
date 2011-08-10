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

var CamliSearch = {};

function getSearchParams() {
	CamliSearch.query = "";
	CamliSearch.type = "";
	CamliSearch.query = getQueryParam('q');
	CamliSearch.type = getQueryParam('t');
}

function hideAllResThings() {
	CamliSearch.titleRes.style.visibility = 'hidden';
	CamliSearch.btnNewCollec.disabled = true;
	CamliSearch.btnNewCollec.style.visibility = 'hidden';
	CamliSearch.formAddToCollec.style.visibility = 'hidden';
}

function handleFormGetTagged(e) {
	e.stopPropagation();
	e.preventDefault();

	var input = document.getElementById("inputTag");

	if (input.value == "") {
		return;
	}

	var tags = input.value.split(/\s*,\s*/);
	document.location.href = "search.html?q=" + tags[0] + "&t=tag"
}

function doSearch() {
	var sigcb = {};
	sigcb.success = function(sigconf) {
		var tagcb = {};
		tagcb.success = function(pres) {
			showSearchResult(pres, CamliSearch.type);
		};
//TODO(mpl): add other kinds of searches (by filename for ex).
		switch(CamliSearch.type) {
		case "tag":
			camliGetTaggedPermanodes(sigconf.publicKeyBlobRef, CamliSearch.query, tagcb);
			break;
		default:
			break;
		}
	};
	sigcb.fail = function() {
		alert("sig disco failed");
	}
	camliSigDiscovery(sigcb);
}

function showSearchResult(pres, type) {
	switch(type) {
	case "tag":
		showTaggedPermanodes(pres);
		break;
	default:
		break;
	}
	CamliSearch.query = "";
	CamliSearch.type = "";
}

function showTaggedPermanodes(searchRes) {
	var div = document.getElementById("divRes");
	while (div.hasChildNodes()) {
		div.removeChild(div.lastChild);
	}
	if (searchRes.tagged.length > 0) {
		var checkall = document.createElement("input");
		checkall.id = "checkall";
		checkall.type = "checkbox";
		checkall.name = "checkall";
		checkall.checked = false;
		checkall.onclick = Function("checkAll();");
		div.appendChild(checkall);
		div.appendChild(document.createElement("br"));
	}
	for (var i = 0; i < searchRes.tagged.length; i++) {
		var result = searchRes.tagged[i];
		var alink = document.createElement("a");
		alink.href = "./?p=" + result.permanode;
		alink.innerText = camliBlobTitle(result.permanode, searchRes);
		var cbox = document.createElement('input');
		cbox.type = "checkbox";
		cbox.name = "checkbox";
		cbox.value = result.permanode;
		div.appendChild(cbox);
		div.appendChild(alink);
		div.appendChild(document.createElement('br'));
	}
	if (searchRes.tagged.length > 0) {
		CamliSearch.titleRes.innerHTML = "Tagged";
		CamliSearch.titleRes.style.visibility = 'visible';
		CamliSearch.btnNewCollec.disabled = false;
		CamliSearch.btnNewCollec.style.visibility = 'visible';
		CamliSearch.formAddToCollec.style.visibility = 'visible';
	} else {
		hideAllResThings();
	}
}

function getTicked() {
	var checkboxes = document.getElementsByName("checkbox");
	CamliSearch.tickedMemb = new Array();
	var j = 0;
	for (var i = 0; i < checkboxes.length; i++) {
		if (checkboxes[i].checked) {
			CamliSearch.tickedMemb[j] = checkboxes[i].value;
			j++;
		}
	}
}

function checkAll() {
	var checkall = document.getElementById("checkall");
	var checkboxes = document.getElementsByName('checkbox');
	for (var i = 0; i < checkboxes.length; i++) {
		checkboxes[i].checked = checkall.checked;
	}
}

function handleCreateNewCollection(e) {
	addToCollection(true)
}

function handleAddToCollection(e) {
	e.stopPropagation();
	e.preventDefault();
	addToCollection(false)
}

function addToCollection(createNew) {
	var cnpcb = {};
	cnpcb.success = function(parent) {
		var nRemain = CamliSearch.tickedMemb.length;
		var naaccb = {};
		naaccb.fail = function() {
			CamliSearch.btnNewCollec.disabled = true;
			throw("failed to add member to collection");
		}
		naaccb.success = function() {
			nRemain--;
			if (nRemain == 0) {
				CamliSearch.btnNewCollec.disabled = true;
				window.location = "./?p=" + parent;
			}
		}
		try {
			for (var i = 0; i < CamliSearch.tickedMemb.length; i++) {
				camliNewAddAttributeClaim(parent, "camliMember", CamliSearch.tickedMemb[i], naaccb);
			}
		} catch(x) {
			alert(x)
		}
	};
	cnpcb.fail = function() {
		alert("failed to create permanode");
	};
	getTicked();
	if (CamliSearch.tickedMemb.length > 0) {
		if (createNew) {
			camliCreateNewPermanode(cnpcb);
		} else {
			var pn = document.getElementById("inputCollec").value;
//TODO(mpl): allow a collection title (instead of a hash) as input
			if (!isPlausibleBlobRef(pn)) {
				alert("Not a valid collection permanode hash");
				return;
			}
			var returnPn = function(opts) {
				opts = saneOpts(opts);
				opts.success(pn);
			}
			returnPn(cnpcb);
		}
	} else {
		alert("No selected object")
	}
}

function indexOnLoad(e) {

	var formTags = document.getElementById("formTags");
	formTags.addEventListener("submit", handleFormGetTagged);
	CamliSearch.titleRes = document.getElementById("titleRes");
	CamliSearch.btnNewCollec = document.getElementById("btnNewCollec");
	CamliSearch.btnNewCollec.addEventListener("click", handleCreateNewCollection);
	CamliSearch.formAddToCollec = document.getElementById("formAddToCollec");
	CamliSearch.formAddToCollec.addEventListener("submit", handleAddToCollection);
	hideAllResThings();
	getSearchParams();
	if (CamliSearch.query != "") {
		document.getElementById("inputTag").value = CamliSearch.query;
		doSearch();
	}
}

window.addEventListener("load", indexOnLoad);
