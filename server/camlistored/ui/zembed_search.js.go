// THIS FILE IS AUTO-GENERATED FROM search.js
// DO NOT EDIT.
package ui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("search.js", 7604, fileembed.String("/*\n"+
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
		"var CamliSearch = {};\n"+
		"\n"+
		"function getSearchParams() {\n"+
		"	CamliSearch.query = \"\";\n"+
		"	CamliSearch.type = \"\";\n"+
		"	CamliSearch.fuzzy = \"\";\n"+
		"	CamliSearch.query = getQueryParam('q') || \"\";\n"+
		"	CamliSearch.type = getQueryParam('t') || \"\";\n"+
		"	CamliSearch.fuzzy = getQueryParam('f') || \"\";\n"+
		"}\n"+
		"\n"+
		"function hideAllResThings() {\n"+
		"	CamliSearch.titleRes.style.visibility = 'hidden';\n"+
		"	CamliSearch.btnNewCollec.disabled = true;\n"+
		"	CamliSearch.btnNewCollec.style.visibility = 'hidden';\n"+
		"	CamliSearch.formAddToCollec.style.visibility = 'hidden';\n"+
		"}\n"+
		"\n"+
		"function handleFormGetRoots(e) {\n"+
		"	e.stopPropagation();\n"+
		"	e.preventDefault();\n"+
		"\n"+
		"	document.location.href = \"search.html?&t=camliRoot\"\n"+
		"}\n"+
		"\n"+
		"function handleFormGetTagged(e) {\n"+
		"	e.stopPropagation();\n"+
		"	e.preventDefault();\n"+
		"\n"+
		"	var input = document.getElementById(\"inputTag\");\n"+
		"\n"+
		"	if (input.value == \"\") {\n"+
		"		return;\n"+
		"	}\n"+
		"\n"+
		"	var tags = input.value.split(/\\s*,\\s*/);\n"+
		"	document.location.href = \"search.html?q=\" + tags[0] + \"&t=tag\"\n"+
		"}\n"+
		"\n"+
		"function handleFormGetTitled(e) {\n"+
		"	e.stopPropagation();\n"+
		"	e.preventDefault();\n"+
		"\n"+
		"	var input = document.getElementById(\"inputTitle\");\n"+
		"\n"+
		"	if (input.value == \"\") {\n"+
		"		return;\n"+
		"	}\n"+
		"\n"+
		"	var titles = input.value.split(/\\s*,\\s*/);\n"+
		"	document.location.href = \"search.html?q=\" + titles[0] + \"&t=title\"\n"+
		"}\n"+
		"\n"+
		"function handleFormGetAnyAttr(e) {\n"+
		"	e.stopPropagation();\n"+
		"	e.preventDefault();\n"+
		"\n"+
		"	var input = document.getElementById(\"inputAnyAttr\");\n"+
		"\n"+
		"	if (input.value == \"\") {\n"+
		"		return;\n"+
		"	}\n"+
		"\n"+
		"	var any = input.value.split(/\\s*,\\s*/);\n"+
		"	document.location.href = \"search.html?q=\" + any[0]\n"+
		"}\n"+
		"\n"+
		"function doSearch() {\n"+
		"	var sigcb = {};\n"+
		"	sigcb.success = function(sigconf) {\n"+
		"		var tagcb = {};\n"+
		"		tagcb.success = function(pres) {\n"+
		"			showSearchResult(pres, CamliSearch.type);\n"+
		"		};\n"+
		"		tagcb.fail = function(msg) {\n"+
		"			alert(msg);\n"+
		"		};\n"+
		"		switch(CamliSearch.type) {\n"+
		"		case \"tag\":\n"+
		"			camliGetPermanodesWithAttr(sigconf.publicKeyBlobRef, \"tag\", CamliSearch.query,"+
		" CamliSearch.fuzzy, tagcb);\n"+
		"			break;\n"+
		"		case \"title\":\n"+
		"			camliGetPermanodesWithAttr(sigconf.publicKeyBlobRef, \"title\", CamliSearch.quer"+
		"y, \"true\", tagcb);\n"+
		"			break;\n"+
		"		case \"camliRoot\":\n"+
		"			camliGetPermanodesWithAttr(sigconf.publicKeyBlobRef, \"camliRoot\", CamliSearch."+
		"query, \"false\", tagcb);\n"+
		"			break;\n"+
		"		case \"\":\n"+
		"			if (CamliSearch.query !== \"\") {\n"+
		"				camliGetPermanodesWithAttr(sigconf.publicKeyBlobRef, \"\", CamliSearch.query, \""+
		"true\", tagcb);\n"+
		"			}\n"+
		"			break;\n"+
		"		}\n"+
		"	};\n"+
		"	sigcb.fail = function() {\n"+
		"		alert(\"sig disco failed\");\n"+
		"	}\n"+
		"	camliSigDiscovery(sigcb);\n"+
		"}\n"+
		"\n"+
		"function showSearchResult(pres, type) {\n"+
		"	showPermanodes(pres, type);\n"+
		"	CamliSearch.query = \"\";\n"+
		"	CamliSearch.type = \"\";\n"+
		"}\n"+
		"\n"+
		"function showPermanodes(searchRes, type) {\n"+
		"	var div = document.getElementById(\"divRes\");\n"+
		"	while (div.hasChildNodes()) {\n"+
		"		div.removeChild(div.lastChild);\n"+
		"	}\n"+
		"	var results = searchRes.withAttr;\n"+
		"	if (results.length > 0) {\n"+
		"		var checkall = document.createElement(\"input\");\n"+
		"		checkall.id = \"checkall\";\n"+
		"		checkall.type = \"checkbox\";\n"+
		"		checkall.name = \"checkall\";\n"+
		"		checkall.checked = false;\n"+
		"		checkall.onclick = Function(\"checkAll();\");\n"+
		"		div.appendChild(checkall);\n"+
		"		div.appendChild(document.createElement(\"br\"));\n"+
		"	}\n"+
		"	for (var i = 0; i < results.length; i++) {\n"+
		"		var result = results[i];\n"+
		"		var alink = document.createElement(\"a\");\n"+
		"		alink.href = \"./?p=\" + result.permanode;\n"+
		"		setTextContent(alink, camliBlobTitle(result.permanode, searchRes));\n"+
		"		var cbox = document.createElement('input');\n"+
		"		cbox.type = \"checkbox\";\n"+
		"		cbox.name = \"checkbox\";\n"+
		"		cbox.value = result.permanode;\n"+
		"		div.appendChild(cbox);\n"+
		"		div.appendChild(alink);\n"+
		"		div.appendChild(document.createElement('br'));\n"+
		"	}\n"+
		"	if (results.length > 0) {\n"+
		"		switch(type) {\n"+
		"		case \"tag\":\n"+
		"			CamliSearch.titleRes.innerHTML = \"Tagged with \\\"\" + CamliSearch.query + \"\\\"\";\n"+
		"			break;\n"+
		"		case \"title\":\n"+
		"			CamliSearch.titleRes.innerHTML = \"Titled with \\\"\" + CamliSearch.query + \"\\\"\";\n"+
		"			break;\n"+
		"		case \"camliRoot\":\n"+
		"			CamliSearch.titleRes.innerHTML = \"All roots\";\n"+
		"			break;\n"+
		"		case \"\":\n"+
		"			CamliSearch.titleRes.innerHTML = \"General search for \\\"\" + CamliSearch.query +"+
		" \"\\\"\";\n"+
		"			break;\n"+
		"		}\n"+
		"		CamliSearch.titleRes.style.visibility = 'visible';\n"+
		"		CamliSearch.btnNewCollec.disabled = false;\n"+
		"		CamliSearch.btnNewCollec.style.visibility = 'visible';\n"+
		"		CamliSearch.formAddToCollec.style.visibility = 'visible';\n"+
		"	} else {\n"+
		"		hideAllResThings();\n"+
		"	}\n"+
		"}\n"+
		"\n"+
		"function getTicked() {\n"+
		"	var checkboxes = document.getElementsByName(\"checkbox\");\n"+
		"	CamliSearch.tickedMemb = new Array();\n"+
		"	var j = 0;\n"+
		"	for (var i = 0; i < checkboxes.length; i++) {\n"+
		"		if (checkboxes[i].checked) {\n"+
		"			CamliSearch.tickedMemb[j] = checkboxes[i].value;\n"+
		"			j++;\n"+
		"		}\n"+
		"	}\n"+
		"}\n"+
		"\n"+
		"function checkAll() {\n"+
		"	var checkall = document.getElementById(\"checkall\");\n"+
		"	var checkboxes = document.getElementsByName('checkbox');\n"+
		"	for (var i = 0; i < checkboxes.length; i++) {\n"+
		"		checkboxes[i].checked = checkall.checked;\n"+
		"	}\n"+
		"}\n"+
		"\n"+
		"function handleCreateNewCollection(e) {\n"+
		"	addToCollection(true)\n"+
		"}\n"+
		"\n"+
		"function handleAddToCollection(e) {\n"+
		"	e.stopPropagation();\n"+
		"	e.preventDefault();\n"+
		"	addToCollection(false)\n"+
		"}\n"+
		"\n"+
		"function addToCollection(createNew) {\n"+
		"	var cnpcb = {};\n"+
		"	cnpcb.success = function(parent) {\n"+
		"		var nRemain = CamliSearch.tickedMemb.length;\n"+
		"		var naaccb = {};\n"+
		"		naaccb.fail = function() {\n"+
		"			CamliSearch.btnNewCollec.disabled = true;\n"+
		"			throw(\"failed to add member to collection\");\n"+
		"		}\n"+
		"		naaccb.success = function() {\n"+
		"			nRemain--;\n"+
		"			if (nRemain == 0) {\n"+
		"				CamliSearch.btnNewCollec.disabled = true;\n"+
		"				window.location = \"./?p=\" + parent;\n"+
		"			}\n"+
		"		}\n"+
		"		try {\n"+
		"			for (var i = 0; i < CamliSearch.tickedMemb.length; i++) {\n"+
		"				camliNewAddAttributeClaim(parent, \"camliMember\", CamliSearch.tickedMemb[i], n"+
		"aaccb);\n"+
		"			}\n"+
		"		} catch(x) {\n"+
		"			alert(x)\n"+
		"		}\n"+
		"	};\n"+
		"	cnpcb.fail = function() {\n"+
		"		alert(\"failed to create permanode\");\n"+
		"	};\n"+
		"	getTicked();\n"+
		"	if (CamliSearch.tickedMemb.length > 0) {\n"+
		"		if (createNew) {\n"+
		"			camliCreateNewPermanode(cnpcb);\n"+
		"		} else {\n"+
		"			var pn = document.getElementById(\"inputCollec\").value;\n"+
		"//TODO(mpl): allow a collection title (instead of a hash) as input\n"+
		"			if (!isPlausibleBlobRef(pn)) {\n"+
		"				alert(\"Not a valid collection permanode hash\");\n"+
		"				return;\n"+
		"			}\n"+
		"			var returnPn = function(opts) {\n"+
		"				opts = saneOpts(opts);\n"+
		"				opts.success(pn);\n"+
		"			}\n"+
		"			returnPn(cnpcb);\n"+
		"		}\n"+
		"	} else {\n"+
		"		alert(\"No selected object\")\n"+
		"	}\n"+
		"}\n"+
		"\n"+
		"function indexOnLoad(e) {\n"+
		"\n"+
		"	var formRoots = document.getElementById(\"formRoots\");\n"+
		"	formRoots.addEventListener(\"submit\", handleFormGetRoots);\n"+
		"	var formTags = document.getElementById(\"formTags\");\n"+
		"	formTags.addEventListener(\"submit\", handleFormGetTagged);\n"+
		"	var formTitles = document.getElementById(\"formTitles\");\n"+
		"	formTitles.addEventListener(\"submit\", handleFormGetTitled);\n"+
		"	var formAnyAttr = document.getElementById(\"formAnyAttr\");\n"+
		"	formAnyAttr.addEventListener(\"submit\", handleFormGetAnyAttr);\n"+
		"	CamliSearch.titleRes = document.getElementById(\"titleRes\");\n"+
		"	CamliSearch.btnNewCollec = document.getElementById(\"btnNewCollec\");\n"+
		"	CamliSearch.btnNewCollec.addEventListener(\"click\", handleCreateNewCollection);\n"+
		"	CamliSearch.formAddToCollec = document.getElementById(\"formAddToCollec\");\n"+
		"	CamliSearch.formAddToCollec.addEventListener(\"submit\", handleAddToCollection);\n"+
		"	hideAllResThings();\n"+
		"	getSearchParams();\n"+
		"	doSearch();\n"+
		"}\n"+
		"\n"+
		"window.addEventListener(\"load\", indexOnLoad);\n"+
		""), time.Unix(0, 1353028462610912719))
}
