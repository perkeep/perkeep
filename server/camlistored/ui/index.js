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

var CamliIndexPage = {
    thumbSizes: [25, 50, 75, 100, 150, 200],
    thumbSizeIdx: 3
};

CamliIndexPage.thumbSize = function() {
  return CamliIndexPage.thumbSizes[CamliIndexPage.thumbSizeIdx];
};

CamliIndexPage.thumbBoxSize = function() {
  return 50 + CamliIndexPage.thumbSizes[CamliIndexPage.thumbSizeIdx];
};

CamliIndexPage.thumbFontSize = function() {
  var fontSize = (CamliIndexPage.thumbSize() / 6);
  if (fontSize < 10) {
      fontSize = 10;
  }
  if (fontSize > 20) {
      fontSize = 20;
  }
  return fontSize + "px";
};

CamliIndexPage.onLoad = function() {
    CamliIndexPage.startRecentLoading();

    var selView = $("selectView");
    var goTargets = {
      "recent": function() { alert("not implemented, but it's already in recent mode"); },
      "date": function() { alert("TODO: pop up a date selector dialog"); },
      "fromsel": function() { alert("TODO: go forward in time from selected item"); },
      "debug:signing": "signing.html", 
      "debug:disco": "disco.html",
      "debug:misc": "debug.html",
      "search": "search.html"
    };
    selView.addEventListener(
        "change",
        function(e) {
            var target = goTargets[selView.value];
            if (!target) {
                return;
            }
            if (typeof(target) == "string") {
                window.location = target;
            }
            if (typeof(target) == "function") {
                target();
            }
    });

    $("formSearch").addEventListener("submit", CamliIndexPage.onSearchSubmit);
    $("btnSmaller").addEventListener("click", CamliIndexPage.sizeHandler(-1));
    $("btnBigger").addEventListener("click", CamliIndexPage.sizeHandler(1));
    setTextContent($("topTitle"), Camli.config.ownerName + "'s Vault");
};

CamliIndexPage.sizeHandler = function(idxDelta) {
    return function(e) { // onclick handler
        var newSize = CamliIndexPage.thumbSizeIdx + idxDelta;
        if (newSize < 0 || newSize >= CamliIndexPage.thumbSizes.length) {
            return;
        }
        CamliIndexPage.thumbSizeIdx = newSize;
        $("recent").innerHTML = "";
        CamliIndexPage.startRecentLoading();
    };
};

CamliIndexPage.startRecentLoading = function() {
    camliGetRecentlyUpdatedPermanodes({success: CamliIndexPage.onLoadedRecentItems, thumbnails: CamliIndexPage.thumbSize()});
};

CamliIndexPage.onSearchSubmit = function(e) {
    e.preventDefault();
    e.stopPropagation();
    var searchVal = $("textSearch").value;
    if (searchVal == "") {
        CamliIndexPage.startRecentLoading();
    } else {
        // TODO: super lame.  for now.  should just change filter
        // of existing page, without navigating away.
        window.location = "search.html?t=tag&q=" + searchVal;
    }
};

var lastSelIndex = 0;
var selSetter = {};         // numeric index -> func(selected) setter
var currentlySelected = {}; // currently selected index -> true
var itemsSelected = 0;

CamliIndexPage.setThumbBoxStyle = function(div) {
  div.style.width = CamliIndexPage.thumbBoxSize() + "px";
  div.style.height = CamliIndexPage.thumbBoxSize() + "px";
  div.style.maxWidth = CamliIndexPage.thumbBoxSize() + "px";
  div.style.maxHeight = CamliIndexPage.thumbBoxSize() + "px";
};

// divFromResult converts the |i|th searchResult into
// a div element, style as a thumbnail tile.
function divFromResult(searchRes, i) {
    var result = searchRes.recent[i];
    var br = searchRes[result.blobref];
    var divperm = document.createElement("div");
    CamliIndexPage.setThumbBoxStyle(divperm);

    var setSelected = function(selected) {
        if (divperm.isSelected == selected) {
            return;
        }
	divperm.isSelected = selected;
	if (selected) {
	    lastSelIndex = i;
	    currentlySelected[i] = true;
	    divperm.classList.add("selected");
	} else {
	    delete currentlySelected[selected];
	    lastSelIndex = -1;
	    divperm.classList.remove("selected");
	}
        itemsSelected += selected ? 1 : -1;
        $("optFromSel").disabled = (itemsSelected == 0);
    };
    selSetter[i] = setSelected;
    divperm.addEventListener(
        "mousedown", function(e) {
	    if (e.shiftKey) {
	        e.preventDefault(); // prevent browser range selection
	    }
	});
    divperm.addEventListener(
        "click", function(e) {
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
    title.style.fontSize = CamliIndexPage.thumbFontSize();
    divperm.appendChild(title);
    divperm.className = 'camli-ui-thumb';
    return divperm;
}

// createPlusButton returns the div element that is both a button
// a drop zone for new file(s).
function createPlusButton() {
  var div = document.createElement("div");
  div.id = "plusdrop";
  div.className = "camli-ui-thumb";
  CamliIndexPage.setThumbBoxStyle(div);

  var plusLink = document.createElement("a");
  plusLink.classList.add("plusLink");
  plusLink.href = '#';
  plusLink.innerHTML = "+";

  plusLink.style.fontSize = (CamliIndexPage.thumbSize() / 4 * 3) + "px";
  plusLink.style.marginTop = (CamliIndexPage.thumbSize() / 4) + "px";
  div.appendChild(plusLink);

  var statusDiv = document.createElement("div");
  statusDiv.innerHTML = "Click or drag & drop files here.";
  statusDiv.style.fontSize = CamliIndexPage.thumbFontSize();

  // TODO: use statusDiv instead (hidden by default), but put
  // it somewhere users can get to it with a click.
  div.appendChild(statusDiv);

  plusLink.addEventListener("click", function(e) {
      e.preventDefault();
      camliCreateNewPermanode({
            success: function(blobref) {
               window.location = "./?p=" + blobref;
            },
            fail: function(msg) {
                alert("create permanode failed: " + msg);
            }
        });
  });
  
  var stop = function(e) {
    this.classList && this.classList.add('camli-dnd-over');
    e.stopPropagation();
    e.preventDefault();
  };
  div.addEventListener("dragenter", stop, false);
  div.addEventListener("dragover", stop, false);
  div.addEventListener("dragleave", function() {
      this.classList.remove('camli-dnd-over');
  }, false);

  var drop = function(e) {
    this.classList.remove('camli-dnd-over');
    stop(e);
    var dt = e.dataTransfer;
    var files = dt.files;
    var subject = "";
    if (files.length == 1) {
      subject = files[0].name;
    } else {
      subject = files.length + " files";
    }
    statusDiv.innerHTML = "Uploading " + subject + " (<a href='#'>status</a>)";
    startFileUploads(files, document.getElementById("debugstatus"), {
      success: function() {
          statusDiv.innerHTML = "Uploaded.";

          // TODO(bradfitz): this just re-does the whole initial
          // query, and only at the very end of all the uploads.
          // it would be cooler if, when uploading a dozen
          // large files, we saw the permanodes load in one-at-a-time
          // as the became available.
          CamliIndexPage.startRecentLoading();
      }
    });
  };
  div.addEventListener("drop", drop, false);
  return div;
}

// files: array of File objects to upload and create permanods for.
//    If >1, also create an enclosing permanode for them to all
//    be members of.
// statusdiv: optional div element to log status messages to.
// opts:
// -- success: function([permanodes])
function startFileUploads(files, statusDiv, opts) {
  var parentNode = opts.parentNode;
  if (files.length > 1 && !parentNode) {
    // create a new parent permanode with dummy
    // title and re-call startFileUploads with
    // opts.parentNode set, so we upload into that.
  }

  var log = function(msg) {
    if (statusDiv) {
      var p = document.createElement("p");
      p.innerHTML = msg;
      statusDiv.appendChild(p);
    }
  };

  var remain = files.length;
  log("Need to upload " + remain + " files");

  var permanodes = [];
  var fails = [];
  var decr = function() {
    remain--;
    log(remain + " remaining now");
    if (remain > 0) {
      return;
    }
    if (fails.length > 0) {
      if (opts.fail) {
        opts.fail(fails);
      }
      return
    }
    if (permanodes.length == files.length) {
      if (opts.success) {
        opts.success();
      }
    }
  };
  var permanodeGood = function(permaRef, fileRef) {
    log("File succeeeded: file=" + fileRef + " permanode=" + permaRef);
    permanodes.push(permaRef);
    decr();
  };
  var fileFail = function(msg) {
    log("File failed: " + msg);
    fails.push(msg);
    decr();
  };
  var fileSuccess = function(fileRef) {
    camliCreateNewPermanode({
      success: function(filepn) {
          camliNewSetAttributeClaim(filepn, "camliContent", fileRef, {
            success: function() {
                permanodeGood(filepn, fileRef);
            },
            fail: fileFail
            });
        }
    });
  };
  
  // TODO(bradfitz): do something smarter than starting all at once.
  // Only keep n in flight or something?
  for (var i = 0; i < files.length; i++) {
    camliUploadFile(files[i], {
      success: fileSuccess, 
      fail: fileFail
    });
  }
}

CamliIndexPage.onLoadedRecentItems = function (searchRes) {
    var divrecent = $("recent");
    divrecent.innerHTML = "";
    divrecent.appendChild(createPlusButton());
    for (var i = 0; i < searchRes.recent.length; i++) {
	divrecent.appendChild(divFromResult(searchRes, i));
    }
};

window.addEventListener("load", CamliIndexPage.onLoad);
