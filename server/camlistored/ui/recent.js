/*
Copyright 2012 Camlistore Authors.

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

function indexOnLoad(e) {
	camliGetRecentlyUpdatedPermanodes({success: indexBuildRecentlyUpdatedPermanodes, thumbnails: true});
}

function indexBuildRecentlyUpdatedPermanodes(searchRes) {
	var divrecent = document.getElementById("recent");
	divrecent.innerHTML = "";
	for (var i = 0; i < searchRes.recent.length; i++) {
		var result = searchRes.recent[i];
		var br = searchRes[result.blobref];
		var divperm = document.createElement("div");
		var alink = document.createElement("a");
		alink.href = "./?p=" + br.blobRef;
		var img = document.createElement("img");
		img.src = br.thumbnailSrc;
		img.height = br.thumbnailHeight;
		img.width =  br.thumbnailWidth;
		alink.appendChild(img);
		divperm.appendChild(alink);
		var title = document.createElement("p");
		setTextContent(title, camliBlobTitle(br.blobRef, searchRes));
		title.className = 'camli-ui-thumbtitle';
		divperm.appendChild(title);
		divperm.className = 'camli-ui-thumb';
		divrecent.appendChild(divperm);
	}
}

window.addEventListener("load", indexOnLoad);
