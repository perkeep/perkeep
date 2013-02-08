// THIS FILE IS AUTO-GENERATED FROM index.js
// DO NOT EDIT.
package ui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("index.js", 10853, fileembed.String("/*\n"+
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
		"var CamliIndexPage = {\n"+
		"    thumbSizes: [25, 50, 75, 100, 150, 200],\n"+
		"    thumbSizeIdx: 3\n"+
		"};\n"+
		"\n"+
		"CamliIndexPage.thumbSize = function() {\n"+
		"  return CamliIndexPage.thumbSizes[CamliIndexPage.thumbSizeIdx];\n"+
		"};\n"+
		"\n"+
		"CamliIndexPage.thumbBoxSize = function() {\n"+
		"  return 50 + CamliIndexPage.thumbSizes[CamliIndexPage.thumbSizeIdx];\n"+
		"};\n"+
		"\n"+
		"CamliIndexPage.thumbFontSize = function() {\n"+
		"  var fontSize = (CamliIndexPage.thumbSize() / 6);\n"+
		"  if (fontSize < 10) {\n"+
		"      fontSize = 10;\n"+
		"  }\n"+
		"  if (fontSize > 20) {\n"+
		"      fontSize = 20;\n"+
		"  }\n"+
		"  return fontSize + \"px\";\n"+
		"};\n"+
		"\n"+
		"CamliIndexPage.onLoad = function() {\n"+
		"    CamliIndexPage.startRecentLoading();\n"+
		"\n"+
		"    var selView = $(\"selectView\");\n"+
		"    var goTargets = {\n"+
		"      \"recent\": function() { alert(\"not implemented, but it's already in recent m"+
		"ode\"); },\n"+
		"      \"date\": function() { alert(\"TODO: pop up a date selector dialog\"); },\n"+
		"      \"fromsel\": function() { alert(\"TODO: go forward in time from selected item\""+
		"); },\n"+
		"      \"debug:signing\": \"signing.html\", \n"+
		"      \"debug:disco\": \"disco.html\",\n"+
		"      \"debug:misc\": \"debug.html\",\n"+
		"      \"search\": \"search.html\"\n"+
		"    };\n"+
		"    selView.addEventListener(\n"+
		"        \"change\",\n"+
		"        function(e) {\n"+
		"            var target = goTargets[selView.value];\n"+
		"            if (!target) {\n"+
		"                return;\n"+
		"            }\n"+
		"            if (typeof(target) == \"string\") {\n"+
		"                window.location = target;\n"+
		"            }\n"+
		"            if (typeof(target) == \"function\") {\n"+
		"                target();\n"+
		"            }\n"+
		"    });\n"+
		"\n"+
		"    $(\"formSearch\").addEventListener(\"submit\", CamliIndexPage.onSearchSubmit);\n"+
		"    $(\"btnSmaller\").addEventListener(\"click\", CamliIndexPage.sizeHandler(-1));\n"+
		"    $(\"btnBigger\").addEventListener(\"click\", CamliIndexPage.sizeHandler(1));\n"+
		"    Camli.setTextContent($(\"topTitle\"), Camli.config.ownerName + \"'s Vault\");\n"+
		"};\n"+
		"\n"+
		"CamliIndexPage.sizeHandler = function(idxDelta) {\n"+
		"    return function(e) { // onclick handler\n"+
		"        var newSize = CamliIndexPage.thumbSizeIdx + idxDelta;\n"+
		"        if (newSize < 0 || newSize >= CamliIndexPage.thumbSizes.length) {\n"+
		"            return;\n"+
		"        }\n"+
		"        CamliIndexPage.thumbSizeIdx = newSize;\n"+
		"        $(\"recent\").innerHTML = \"\";\n"+
		"        CamliIndexPage.startRecentLoading();\n"+
		"    };\n"+
		"};\n"+
		"\n"+
		"CamliIndexPage.startRecentLoading = function() {\n"+
		"    camliGetRecentlyUpdatedPermanodes({success: CamliIndexPage.onLoadedRecentItem"+
		"s, thumbnails: CamliIndexPage.thumbSize()});\n"+
		"};\n"+
		"\n"+
		"CamliIndexPage.onSearchSubmit = function(e) {\n"+
		"    e.preventDefault();\n"+
		"    e.stopPropagation();\n"+
		"    var searchVal = $(\"textSearch\").value;\n"+
		"    if (searchVal == \"\") {\n"+
		"        CamliIndexPage.startRecentLoading();\n"+
		"    } else {\n"+
		"        // TODO: super lame.  for now.  should just change filter\n"+
		"        // of existing page, without navigating away.\n"+
		"        window.location = \"search.html?t=tag&q=\" + searchVal;\n"+
		"    }\n"+
		"};\n"+
		"\n"+
		"var lastSelIndex = 0;\n"+
		"var selSetter = {};         // numeric index -> func(selected) setter\n"+
		"var currentlySelected = {}; // currently selected index -> true\n"+
		"var itemsSelected = 0;\n"+
		"\n"+
		"CamliIndexPage.setThumbBoxStyle = function(div) {\n"+
		"  div.style.width = CamliIndexPage.thumbBoxSize() + \"px\";\n"+
		"  div.style.height = CamliIndexPage.thumbBoxSize() + \"px\";\n"+
		"  div.style.maxWidth = CamliIndexPage.thumbBoxSize() + \"px\";\n"+
		"  div.style.maxHeight = CamliIndexPage.thumbBoxSize() + \"px\";\n"+
		"};\n"+
		"\n"+
		"// divFromResult converts the |i|th searchResult into\n"+
		"// a div element, style as a thumbnail tile.\n"+
		"function divFromResult(searchRes, i) {\n"+
		"    var result = searchRes.recent[i];\n"+
		"    var br = searchRes.meta[result.blobref];\n"+
		"    var divperm = document.createElement(\"div\");\n"+
		"    CamliIndexPage.setThumbBoxStyle(divperm);\n"+
		"\n"+
		"    var setSelected = function(selected) {\n"+
		"        if (divperm.isSelected == selected) {\n"+
		"            return;\n"+
		"        }\n"+
		"	divperm.isSelected = selected;\n"+
		"	if (selected) {\n"+
		"	    lastSelIndex = i;\n"+
		"	    currentlySelected[i] = true;\n"+
		"	    divperm.classList.add(\"selected\");\n"+
		"	} else {\n"+
		"	    delete currentlySelected[selected];\n"+
		"	    lastSelIndex = -1;\n"+
		"	    divperm.classList.remove(\"selected\");\n"+
		"	}\n"+
		"        itemsSelected += selected ? 1 : -1;\n"+
		"        $(\"optFromSel\").disabled = (itemsSelected == 0);\n"+
		"    };\n"+
		"    selSetter[i] = setSelected;\n"+
		"    divperm.addEventListener(\n"+
		"        \"mousedown\", function(e) {\n"+
		"	    if (e.shiftKey) {\n"+
		"	        e.preventDefault(); // prevent browser range selection\n"+
		"	    }\n"+
		"	});\n"+
		"    divperm.addEventListener(\n"+
		"        \"click\", function(e) {\n"+
		"	    if (e.ctrlKey) {\n"+
		"		setSelected(!divperm.isSelected);\n"+
		"		return;\n"+
		"	    }\n"+
		"	    if (e.shiftKey) {\n"+
		"		if (lastSelIndex < 0) {\n"+
		"		    return;\n"+
		"		}\n"+
		"		var from = lastSelIndex;\n"+
		"		var to = i;\n"+
		"		if (to < from) {\n"+
		"		    from = i;\n"+
		"		    to = lastSelIndex;\n"+
		"		}\n"+
		"		for (var j = from; j <= to; j++) {\n"+
		"		    selSetter[j](true);\n"+
		"		}\n"+
		"		return;\n"+
		"	    }\n"+
		"	    for (var j in currentlySelected) {\n"+
		"		if (j != i) {\n"+
		"		    selSetter[j](false);\n"+
		"		}\n"+
		"	    }\n"+
		"	    setSelected(!divperm.isSelected);\n"+
		"	});\n"+
		"    var alink = document.createElement(\"a\");\n"+
		"    alink.href = \"./?p=\" + br.blobRef;\n"+
		"    var img = document.createElement(\"img\");\n"+
		"    img.src = br.thumbnailSrc;\n"+
		"    img.height = br.thumbnailHeight;\n"+
		"    img.width =  br.thumbnailWidth;\n"+
		"    alink.appendChild(img);\n"+
		"    divperm.appendChild(alink);\n"+
		"    var title = document.createElement(\"p\");\n"+
		"    Camli.setTextContent(title, camliBlobTitle(br.blobRef, searchRes.meta));\n"+
		"    title.className = 'camli-ui-thumbtitle';\n"+
		"    title.style.fontSize = CamliIndexPage.thumbFontSize();\n"+
		"    divperm.appendChild(title);\n"+
		"    divperm.className = 'camli-ui-thumb';\n"+
		"    return divperm;\n"+
		"}\n"+
		"\n"+
		"// createPlusButton returns the div element that is both a button\n"+
		"// a drop zone for new file(s).\n"+
		"function createPlusButton() {\n"+
		"  var div = document.createElement(\"div\");\n"+
		"  div.id = \"plusdrop\";\n"+
		"  div.className = \"camli-ui-thumb\";\n"+
		"  CamliIndexPage.setThumbBoxStyle(div);\n"+
		"\n"+
		"  var plusLink = document.createElement(\"a\");\n"+
		"  plusLink.classList.add(\"plusLink\");\n"+
		"  plusLink.href = '#';\n"+
		"  plusLink.innerHTML = \"+\";\n"+
		"\n"+
		"  plusLink.style.fontSize = (CamliIndexPage.thumbSize() / 4 * 3) + \"px\";\n"+
		"  plusLink.style.marginTop = (CamliIndexPage.thumbSize() / 4) + \"px\";\n"+
		"  div.appendChild(plusLink);\n"+
		"\n"+
		"  var statusDiv = document.createElement(\"div\");\n"+
		"  statusDiv.innerHTML = \"Click or drag & drop files here.\";\n"+
		"  statusDiv.style.fontSize = CamliIndexPage.thumbFontSize();\n"+
		"\n"+
		"  // TODO: use statusDiv instead (hidden by default), but put\n"+
		"  // it somewhere users can get to it with a click.\n"+
		"  div.appendChild(statusDiv);\n"+
		"\n"+
		"  plusLink.addEventListener(\"click\", function(e) {\n"+
		"      e.preventDefault();\n"+
		"      camliCreateNewPermanode({\n"+
		"            success: function(blobref) {\n"+
		"               window.location = \"./?p=\" + blobref;\n"+
		"            },\n"+
		"            fail: function(msg) {\n"+
		"                alert(\"create permanode failed: \" + msg);\n"+
		"            }\n"+
		"        });\n"+
		"  });\n"+
		"  \n"+
		"  var stop = function(e) {\n"+
		"    this.classList && this.classList.add('camli-dnd-over');\n"+
		"    e.stopPropagation();\n"+
		"    e.preventDefault();\n"+
		"  };\n"+
		"  div.addEventListener(\"dragenter\", stop, false);\n"+
		"  div.addEventListener(\"dragover\", stop, false);\n"+
		"  div.addEventListener(\"dragleave\", function() {\n"+
		"      this.classList.remove('camli-dnd-over');\n"+
		"  }, false);\n"+
		"\n"+
		"  var drop = function(e) {\n"+
		"    this.classList.remove('camli-dnd-over');\n"+
		"    stop(e);\n"+
		"    var dt = e.dataTransfer;\n"+
		"    var files = dt.files;\n"+
		"    var subject = \"\";\n"+
		"    if (files.length == 1) {\n"+
		"      subject = files[0].name;\n"+
		"    } else {\n"+
		"      subject = files.length + \" files\";\n"+
		"    }\n"+
		"    statusDiv.innerHTML = \"Uploading \" + subject + \" (<a href='#'>status</a>)\";\n"+
		"    startFileUploads(files, document.getElementById(\"debugstatus\"), {\n"+
		"      success: function() {\n"+
		"          statusDiv.innerHTML = \"Uploaded.\";\n"+
		"\n"+
		"          // TODO(bradfitz): this just re-does the whole initial\n"+
		"          // query, and only at the very end of all the uploads.\n"+
		"          // it would be cooler if, when uploading a dozen\n"+
		"          // large files, we saw the permanodes load in one-at-a-time\n"+
		"          // as the became available.\n"+
		"          CamliIndexPage.startRecentLoading();\n"+
		"      }\n"+
		"    });\n"+
		"  };\n"+
		"  div.addEventListener(\"drop\", drop, false);\n"+
		"  return div;\n"+
		"}\n"+
		"\n"+
		"// files: array of File objects to upload and create permanods for.\n"+
		"//    If >1, also create an enclosing permanode for them to all\n"+
		"//    be members of.\n"+
		"// statusdiv: optional div element to log status messages to.\n"+
		"// opts:\n"+
		"// -- success: function([permanodes])\n"+
		"function startFileUploads(files, statusDiv, opts) {\n"+
		"  var parentNode = opts.parentNode;\n"+
		"  if (files.length > 1 && !parentNode) {\n"+
		"    // create a new parent permanode with dummy\n"+
		"    // title and re-call startFileUploads with\n"+
		"    // opts.parentNode set, so we upload into that.\n"+
		"  }\n"+
		"\n"+
		"  var log = function(msg) {\n"+
		"    if (statusDiv) {\n"+
		"      var p = document.createElement(\"p\");\n"+
		"      p.innerHTML = msg;\n"+
		"      statusDiv.appendChild(p);\n"+
		"    }\n"+
		"  };\n"+
		"\n"+
		"  var remain = files.length;\n"+
		"  log(\"Need to upload \" + remain + \" files\");\n"+
		"\n"+
		"  var permanodes = [];\n"+
		"  var fails = [];\n"+
		"  var decr = function() {\n"+
		"    remain--;\n"+
		"    log(remain + \" remaining now\");\n"+
		"    if (remain > 0) {\n"+
		"      return;\n"+
		"    }\n"+
		"    if (fails.length > 0) {\n"+
		"      if (opts.fail) {\n"+
		"        opts.fail(fails);\n"+
		"      }\n"+
		"      return\n"+
		"    }\n"+
		"    if (permanodes.length == files.length) {\n"+
		"      if (opts.success) {\n"+
		"        opts.success();\n"+
		"      }\n"+
		"    }\n"+
		"  };\n"+
		"  var permanodeGood = function(permaRef, fileRef) {\n"+
		"    log(\"File succeeeded: file=\" + fileRef + \" permanode=\" + permaRef);\n"+
		"    permanodes.push(permaRef);\n"+
		"    decr();\n"+
		"  };\n"+
		"  var fileFail = function(msg) {\n"+
		"    log(\"File failed: \" + msg);\n"+
		"    fails.push(msg);\n"+
		"    decr();\n"+
		"  };\n"+
		"  var fileSuccess = function(fileRef) {\n"+
		"    camliCreateNewPermanode({\n"+
		"      success: function(filepn) {\n"+
		"          camliNewSetAttributeClaim(filepn, \"camliContent\", fileRef, {\n"+
		"            success: function() {\n"+
		"                permanodeGood(filepn, fileRef);\n"+
		"            },\n"+
		"            fail: fileFail\n"+
		"            });\n"+
		"        }\n"+
		"    });\n"+
		"  };\n"+
		"  \n"+
		"  // TODO(bradfitz): do something smarter than starting all at once.\n"+
		"  // Only keep n in flight or something?\n"+
		"  for (var i = 0; i < files.length; i++) {\n"+
		"    camliUploadFile(files[i], {\n"+
		"      success: fileSuccess, \n"+
		"      fail: fileFail\n"+
		"    });\n"+
		"  }\n"+
		"}\n"+
		"\n"+
		"CamliIndexPage.onLoadedRecentItems = function (searchRes) {\n"+
		"    var divrecent = $(\"recent\");\n"+
		"    divrecent.innerHTML = \"\";\n"+
		"    divrecent.appendChild(createPlusButton());\n"+
		"    for (var i = 0; i < searchRes.recent.length; i++) {\n"+
		"	divrecent.appendChild(divFromResult(searchRes, i));\n"+
		"    }\n"+
		"};\n"+
		"\n"+
		"window.addEventListener(\"load\", CamliIndexPage.onLoad);\n"+
		""), time.Unix(0, 1360259901380858573))
}
