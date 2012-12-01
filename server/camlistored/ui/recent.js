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
	camliGetRecentlyUpdatedPermanodes({success: indexBuildRecentlyUpdatedPermanodes, thumbnails: 150});
}

var lastSelIndex = 0;
var selSetter = {};         // numeric index -> func(selected) setter
var currentlySelected = {}; // currently selected index -> true

function divFromResult(searchRes, i) {
	var result = searchRes.recent[i];
	var br = searchRes[result.blobref];
	var divperm = document.createElement("div");
	var setSelected = function(selected) {
		divperm.isSelected = selected;
		if (selected) {
			lastSelIndex = i;
			currentlySelected[i] = true;
			divperm.attributes.class.value = "camli-ui-thumb selected";
		} else {
			delete currentlySelected[selected];
			lastSelIndex = -1;
			divperm.attributes.class.value = "camli-ui-thumb";
		}
	};
	selSetter[i] = setSelected;
	divperm.addEventListener("mousedown", function(e) {
	   if (e.shiftKey) {
		   e.preventDefault(); // prevent browser range selection
	   }
	});
	divperm.addEventListener("click", function(e) {
		if (e.ctrlKey) {
			setSelected(!divperm.isSelected);
			return;
		}
		if (e.shiftKey) {
			if (lastSelIndex < 0) {
				return;
			}
			var from = lastSelIndex;
			var to = i;
			if (to < from) {
				from = i;
				to = lastSelIndex;
			}
			for (var j = from; j <= to; j++) {
				selSetter[j](true);
			}
			return;
		}
		for (var j in currentlySelected) {
			if (j != i) {
				selSetter[j](false);
			}
		}
		setSelected(!divperm.isSelected);
	});
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
	return divperm;
}

function indexBuildRecentlyUpdatedPermanodes(searchRes) {
	var divrecent = document.getElementById("recent");
	divrecent.innerHTML = "";
	for (var i = 0; i < searchRes.recent.length; i++) {
				divrecent.appendChild(divFromResult(searchRes, i));
	}
}

window.addEventListener("load", indexOnLoad);
