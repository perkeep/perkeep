// THIS FILE IS AUTO-GENERATED FROM recent.js
// DO NOT EDIT.
package ui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("recent.js", 2827, fileembed.String("/*\n"+
		"Copyright 2012 Camlistore Authors.\n"+
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
		"function indexOnLoad(e) {\n"+
		"	camliGetRecentlyUpdatedPermanodes({success: indexBuildRecentlyUpdatedPermanodes,"+
		" thumbnails: 150});\n"+
		"}\n"+
		"\n"+
		"var lastSelIndex = 0;\n"+
		"var selSetter = {};         // numeric index -> func(selected) setter\n"+
		"var currentlySelected = {}; // currently selected index -> true\n"+
		"\n"+
		"function divFromResult(searchRes, i) {\n"+
		"	var result = searchRes.recent[i];\n"+
		"	var br = searchRes[result.blobref];\n"+
		"	var divperm = document.createElement(\"div\");\n"+
		"	var setSelected = function(selected) {\n"+
		"		divperm.isSelected = selected;\n"+
		"		if (selected) {\n"+
		"			lastSelIndex = i;\n"+
		"			currentlySelected[i] = true;\n"+
		"			divperm.attributes.class.value = \"camli-ui-thumb selected\";\n"+
		"		} else {\n"+
		"			delete currentlySelected[selected];\n"+
		"			lastSelIndex = -1;\n"+
		"			divperm.attributes.class.value = \"camli-ui-thumb\";\n"+
		"		}\n"+
		"	};\n"+
		"	selSetter[i] = setSelected;\n"+
		"	divperm.addEventListener(\"mousedown\", function(e) {\n"+
		"	   if (e.shiftKey) {\n"+
		"		   e.preventDefault(); // prevent browser range selection\n"+
		"	   }\n"+
		"	});\n"+
		"	divperm.addEventListener(\"click\", function(e) {\n"+
		"		if (e.ctrlKey) {\n"+
		"			setSelected(!divperm.isSelected);\n"+
		"			return;\n"+
		"		}\n"+
		"		if (e.shiftKey) {\n"+
		"			if (lastSelIndex < 0) {\n"+
		"				return;\n"+
		"			}\n"+
		"			var from = lastSelIndex;\n"+
		"			var to = i;\n"+
		"			if (to < from) {\n"+
		"				from = i;\n"+
		"				to = lastSelIndex;\n"+
		"			}\n"+
		"			for (var j = from; j <= to; j++) {\n"+
		"				selSetter[j](true);\n"+
		"			}\n"+
		"			return;\n"+
		"		}\n"+
		"		for (var j in currentlySelected) {\n"+
		"			if (j != i) {\n"+
		"				selSetter[j](false);\n"+
		"			}\n"+
		"		}\n"+
		"		setSelected(!divperm.isSelected);\n"+
		"	});\n"+
		"	var alink = document.createElement(\"a\");\n"+
		"	alink.href = \"./?p=\" + br.blobRef;\n"+
		"	var img = document.createElement(\"img\");\n"+
		"	img.src = br.thumbnailSrc;\n"+
		"	img.height = br.thumbnailHeight;\n"+
		"	img.width =  br.thumbnailWidth;\n"+
		"	alink.appendChild(img);\n"+
		"	divperm.appendChild(alink);\n"+
		"	var title = document.createElement(\"p\");\n"+
		"	setTextContent(title, camliBlobTitle(br.blobRef, searchRes));\n"+
		"	title.className = 'camli-ui-thumbtitle';\n"+
		"	divperm.appendChild(title);\n"+
		"	divperm.className = 'camli-ui-thumb';\n"+
		"	return divperm;\n"+
		"}\n"+
		"\n"+
		"function indexBuildRecentlyUpdatedPermanodes(searchRes) {\n"+
		"	var divrecent = document.getElementById(\"recent\");\n"+
		"	divrecent.innerHTML = \"\";\n"+
		"	for (var i = 0; i < searchRes.recent.length; i++) {\n"+
		"				divrecent.appendChild(divFromResult(searchRes, i));\n"+
		"	}\n"+
		"}\n"+
		"\n"+
		"window.addEventListener(\"load\", indexOnLoad);\n"+
		""), time.Unix(0, 1354385041000000000))
}
