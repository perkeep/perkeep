// THIS FILE IS AUTO-GENERATED FROM home.js
// DO NOT EDIT.
package ui

import "time"

func init() {
	Files.Add("home.js", "/*\n"+
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
		"// CamliHome namespace to contain the global vars\n"+
		"var CamliHome = {};\n"+
		"\n"+
		"function btnCreateNewPermanode(e) {\n"+
		"    camliCreateNewPermanode(\n"+
		"        {\n"+
		"            success: function(blobref) {\n"+
		"               window.location = \"./?p=\" + blobref;\n"+
		"            },\n"+
		"            fail: function(msg) {\n"+
		"                alert(\"create permanode failed: \" + msg);\n"+
		"            }\n"+
		"        });\n"+
		"}\n"+
		"\n"+
		"function handleFormSearch(e) {\n"+
		"    e.stopPropagation();\n"+
		"    e.preventDefault();\n"+
		"\n"+
		"    var input = document.getElementById(\"inputSearch\");\n"+
		"    var btn = document.getElementById(\"btnSearch\");\n"+
		"\n"+
		"    if (input.value == \"\") {\n"+
		"        return;\n"+
		"    }\n"+
		"\n"+
		"    var query = input.value.split(/\\s*,\\s*/);\n"+
		"    window.location = \"./search.html?q=\" + query[0] + \"&t=tag\";\n"+
		"}\n"+
		"\n"+
		"function indexOnLoad(e) {\n"+
		"    var btnNew = document.getElementById(\"btnNew\");\n"+
		"    if (!btnNew) {\n"+
		"        alert(\"missing btnNew\");\n"+
		"    }\n"+
		"    btnNew.addEventListener(\"click\", btnCreateNewPermanode);\n"+
		"    camliGetRecentlyUpdatedPermanodes({ success: indexBuildRecentlyUpdatedPermano"+
		"des });\n"+
		"    var formSearch = document.getElementById(\"formSearch\");\n"+
		"    if (!formSearch) {\n"+
		"        alert(\"missing formSearch\");\n"+
		"    }\n"+
		"    formSearch.addEventListener(\"submit\", handleFormSearch);\n"+
		"\n"+
		"    if (disco && disco.uploadHelper) {\n"+
		"        var uploadForm = document.getElementById(\"uploadform\");\n"+
		"        uploadForm.action = disco.uploadHelper;\n"+
		"        document.getElementById(\"fileinput\").disabled = false;\n"+
		"        document.getElementById(\"filesubmit\").disabled = false;\n"+
		"    }\n"+
		"}\n"+
		"\n"+
		"function indexBuildRecentlyUpdatedPermanodes(searchRes) {\n"+
		"    var ul = document.getElementById(\"recent\");\n"+
		"    ul.innerHTML = \"\";\n"+
		"    for (var i = 0; i < searchRes.recent.length; i++) {\n"+
		"        var result = searchRes.recent[i];      \n"+
		"        var li = document.createElement(\"li\");\n"+
		"        var alink = document.createElement(\"a\");\n"+
		"        alink.href = \"./?p=\" + result.blobref;\n"+
		"        setTextContent(alink, camliBlobTitle(result.blobref, searchRes));\n"+
		"        li.appendChild(alink);\n"+
		"        ul.appendChild(li);\n"+
		"    }\n"+
		"}\n"+
		"\n"+
		"window.addEventListener(\"load\", indexOnLoad);\n"+
		"", time.Unix(0, 1352107488430325498))
}
