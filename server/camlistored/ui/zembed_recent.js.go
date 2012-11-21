// THIS FILE IS AUTO-GENERATED FROM recent.js
// DO NOT EDIT.
package ui

import "time"

func init() {
	Files.Add("recent.js", "/*\n"+
		"Copyright 2012 Camlistore Authors.\n"+
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
		"function indexOnLoad(e) {\n"+
		"	camliGetRecentlyUpdatedPermanodes({success: indexBuildRecentlyUpdatedPermanodes,"+
		" thumbnails: true});\n"+
		"}\n"+
		"\n"+
		"function indexBuildRecentlyUpdatedPermanodes(searchRes) {\n"+
		"	var divrecent = document.getElementById(\"recent\");\n"+
		"	divrecent.innerHTML = \"\";\n"+
		"	for (var i = 0; i < searchRes.recent.length; i++) {\n"+
		"		var result = searchRes.recent[i];\n"+
		"		var br = searchRes[result.blobref];\n"+
		"		var divperm = document.createElement(\"div\");\n"+
		"		var alink = document.createElement(\"a\");\n"+
		"		alink.href = \"./?p=\" + br.blobRef;\n"+
		"		var img = document.createElement(\"img\");\n"+
		"		img.src = br.thumbnailSrc;\n"+
		"		img.height = br.thumbnailHeight;\n"+
		"		img.width =  br.thumbnailWidth;\n"+
		"		alink.appendChild(img);\n"+
		"		divperm.appendChild(alink);\n"+
		"		var title = document.createElement(\"p\");\n"+
		"		setTextContent(title, camliBlobTitle(br.blobRef, searchRes));\n"+
		"		title.className = 'camli-ui-thumbtitle';\n"+
		"		divperm.appendChild(title);\n"+
		"		divperm.className = 'camli-ui-thumb';\n"+
		"		divrecent.appendChild(divperm);\n"+
		"	}\n"+
		"}\n"+
		"\n"+
		"window.addEventListener(\"load\", indexOnLoad);\n"+
		"", time.Unix(0, 1353518413264563890))
}
