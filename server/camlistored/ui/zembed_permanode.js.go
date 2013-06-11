// THIS FILE IS AUTO-GENERATED FROM permanode.js
// DO NOT EDIT.

package newui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("permanode.js", 26532, time.Unix(0, 1370942742232957700), fileembed.String("/*\n"+
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
		"/**\n"+
		" * @fileoverview Permanode description page.\n"+
		" *\n"+
		" */\n"+
		"goog.provide('camlistore.PermanodePage');\n"+
		"\n"+
		"goog.require('goog.dom');\n"+
		"goog.require('goog.events.EventHandler');\n"+
		"goog.require('goog.events.EventType');\n"+
		"goog.require('goog.events.FileDropHandler');\n"+
		"goog.require('goog.ui.Component');\n"+
		"goog.require('camlistore.BlobItem');\n"+
		"goog.require('camlistore.BlobItemContainer');\n"+
		"goog.require('camlistore.ServerConnection');\n"+
		"\n"+
		"/**\n"+
		" * @param {camlistore.ServerType.DiscoveryDocument} config Global config\n"+
		" *   of the current server this page is being rendered for.\n"+
		" * @param {goog.dom.DomHelper=} opt_domHelper DOM helper to use.\n"+
		" *\n"+
		" * @extends {goog.ui.Component}\n"+
		" * @constructor\n"+
		" */\n"+
		"camlistore.PermanodePage = function(config, opt_domHelper) {\n"+
		"	goog.base(this, opt_domHelper);\n"+
		"\n"+
		"	/**\n"+
		"	 * @type {Object}\n"+
		"	 * @private\n"+
		"	 */\n"+
		"	this.config_ = config;\n"+
		"\n"+
		"	/**\n"+
		"	 * For members, not content.\n"+
		"	 * @type {camlistore.BlobItemContainer}\n"+
		"	 * @private\n"+
		"	 */\n"+
		"	this.blobItemContainer_ = new camlistore.BlobItemContainer(\n"+
		"		this.connection_, opt_domHelper\n"+
		"	);\n"+
		"	// We'll get thumbs that are too large for this container, see TODO below.\n"+
		"	// No big deal though.\n"+
		"	this.blobItemContainer_.thumbnailSize_ = camlistore.BlobItemContainer.THUMBNAIL_"+
		"SIZES_[1];\n"+
		"\n"+
		"	/**\n"+
		"	 * @type {camlistore.ServerType.DescribeResponse}\n"+
		"	 * @private\n"+
		"	 */\n"+
		"	this.describeResponse_ = null;\n"+
		"\n"+
		"	/**\n"+
		"	 * @type {camlistore.ServerConnection}\n"+
		"	 * @private\n"+
		"	 */\n"+
		"	this.connection_ = new camlistore.ServerConnection(config);\n"+
		"};\n"+
		"goog.inherits(camlistore.PermanodePage, goog.ui.Component);\n"+
		"\n"+
		"\n"+
		"// TODO(mpl): the problem is that we use that size for the describe request\n"+
		"// without knowing, \xc3\xa0 priori, if we'll get some content (file) or members\n"+
		"// (dir/collection). And if we're with a collection, we'd like way smaller\n"+
		"// thumbs than that.\n"+
		"// We could redo a describe request with a smaller size just to (re)draw the\n"+
		"// members I suppose...\n"+
		"// Salient would probably come in handy here.\n"+
		"/**\n"+
		" * @type {number}\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.PermanodePage.prototype.contentThumbnailSize_ = camlistore.BlobItemCon"+
		"tainer.THUMBNAIL_SIZES_[5];\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * Decorates an existing HTML DIV element.\n"+
		" * @param {Element} element The DIV element to decorate.\n"+
		" */\n"+
		"camlistore.PermanodePage.prototype.decorateInternal = function(element) {\n"+
		"	camlistore.PermanodePage.superClass_.decorateInternal.call(this, element);\n"+
		"\n"+
		"	var el = this.getElement();\n"+
		"	goog.dom.classes.add(el, 'cam-permanode-page');\n"+
		"	\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/** @override */\n"+
		"camlistore.PermanodePage.prototype.disposeInternal = function() {\n"+
		"	camlistore.PermanodePage.superClass_.disposeInternal.call(this);\n"+
		"	this.eh_.dispose();\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * Called when component's element is known to be in the document.\n"+
		" */\n"+
		"camlistore.PermanodePage.prototype.enterDocument = function() {\n"+
		"	camlistore.PermanodePage.superClass_.enterDocument.call(this);\n"+
		"	var permanode = getPermanodeParam();\n"+
		"	if (permanode) {\n"+
		"		goog.dom.getElement('permanode').innerHTML = \"<a href='./?p=\" + permanode + \"'>"+
		"\" + permanode + \"</a>\";\n"+
		"		goog.dom.getElement('permanodeBlob').innerHTML = \"<a href='./?b=\" + permanode +"+
		" \"'>view blob</a>\";\n"+
		"	}\n"+
		"\n"+
		"	// TODO(mpl): use this.eh_ instead?\n"+
		"	// set up listeners\n"+
		"	goog.events.listen(goog.dom.getElement('formTitle'),\n"+
		"		goog.events.EventType.SUBMIT,\n"+
		"		this.handleFormTitleSubmit_,\n"+
		"		false, this);\n"+
		"	goog.events.listen(goog.dom.getElement('formTags'),\n"+
		"		goog.events.EventType.SUBMIT,\n"+
		"		this.handleFormTagsSubmit_,\n"+
		"		false, this);\n"+
		"	goog.events.listen(goog.dom.getElement('formAccess'),\n"+
		"		goog.events.EventType.SUBMIT,\n"+
		"		this.handleFormAccessSubmit_,\n"+
		"		false, this);\n"+
		"	goog.events.listen(goog.dom.getElement('btnGallery'),\n"+
		"		goog.events.EventType.CLICK,\n"+
		"		function() {\n"+
		"			var btnGallery = goog.dom.getElement('btnGallery');\n"+
		"			if (btnGallery.value == \"list\") {\n"+
		"				goog.dom.setTextContent(btnGallery, \"List\");\n"+
		"				btnGallery.value = \"thumbnails\";\n"+
		"			} else {\n"+
		"				goog.dom.setTextContent(btnGallery, \"Thumbnails\");\n"+
		"				btnGallery.value = \"list\";\n"+
		"			}\n"+
		"			this.reloadMembers_();\n"+
		"		},\n"+
		"		false, this);\n"+
		"\n"+
		"	// set publish roots\n"+
		"	this.setupRootsDropdown_();\n"+
		"\n"+
		"	// set dnd and form for file upload\n"+
		"	this.setupFilesHandlers_();\n"+
		"\n"+
		"	this.describeBlob_()\n"+
		"\n"+
		"	this.buildPathsList_()\n"+
		"\n"+
		"	this.blobItemContainer_.render(goog.dom.getElement('membersThumbs'));\n"+
		"};\n"+
		"\n"+
		"// Gets the |p| query parameter, assuming that it looks like a blobref.\n"+
		"function getPermanodeParam() {\n"+
		"	var blobRef = getQueryParam('p');\n"+
		"	return (blobRef && isPlausibleBlobRef(blobRef)) ? blobRef : null;\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * Called when component's element is known to have been removed from the\n"+
		" * document.\n"+
		" */\n"+
		"camlistore.PermanodePage.prototype.exitDocument = function() {\n"+
		"	camlistore.PermanodePage.superClass_.exitDocument.call(this);\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * @param {string} blobRef BlobRef for the uploaded file.\n"+
		" * @param {string} permanode Permanode this blobRef is now the content of.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.PermanodePage.prototype.describeBlob_ = function() {\n"+
		"	var permanode = getPermanodeParam();\n"+
		"	this.connection_.describeWithThumbnails(\n"+
		"		permanode,\n"+
		"		this.contentThumbnailSize_,\n"+
		"		goog.bind(this.handleDescribeBlob_, this, permanode),\n"+
		"		function(msg) {\n"+
		"			alert(\"failed to get blob description: \" + msg);\n"+
		"		}\n"+
		"	);\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * @param {string} permanode Node to describe.\n"+
		" * @param {Object} describeResult Object of properties for the node.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.PermanodePage.prototype.handleDescribeBlob_ =\n"+
		"function(permanode, describeResult) {\n"+
		"	var meta = describeResult.meta;\n"+
		"	if (!meta[permanode]) {\n"+
		"		alert(\"didn't get blob \" + permanode);\n"+
		"		return;\n"+
		"	}\n"+
		"	var permObj = meta[permanode].permanode;\n"+
		"	if (!permObj) {\n"+
		"		alert(\"blob \" + permanode + \" isn't a permanode\");\n"+
		"		return;\n"+
		"	}\n"+
		"	this.describeResponse_ = describeResult;\n"+
		"\n"+
		"	// title form\n"+
		"	var permTitleValue = permAttr(permObj, \"title\") ? permAttr(permObj, \"title\") : \""+
		"\";\n"+
		"	var inputTitle = goog.dom.getElement(\"inputTitle\");\n"+
		"	inputTitle.value = permTitleValue;\n"+
		"	inputTitle.disabled = false;\n"+
		"	var btnSaveTitle = goog.dom.getElement(\"btnSaveTitle\");\n"+
		"	btnSaveTitle.disabled = false;\n"+
		"\n"+
		"	// tags form\n"+
		"	this.reloadTags_(permanode, describeResult);\n"+
		"	var inputNewTag = goog.dom.getElement(\"inputNewTag\");\n"+
		"	inputNewTag.disabled = false;\n"+
		"	var btnAddTag = goog.dom.getElement(\"btnAddTag\");\n"+
		"	btnAddTag.disabled = false;\n"+
		"\n"+
		"	// access form\n"+
		"	var selectAccess = goog.dom.getElement(\"selectAccess\");\n"+
		"	var accessValue = permAttr(permObj,\"camliAccess\") ? permAttr(permObj,\"camliAcces"+
		"s\") : \"private\";\n"+
		"	selectAccess.value = accessValue;\n"+
		"	selectAccess.disabled = false;\n"+
		"	var btnSaveAccess = goog.dom.getElement(\"btnSaveAccess\");\n"+
		"	btnSaveAccess.disabled = false;\n"+
		"\n"+
		"	// handle type detection\n"+
		"	handleType(permObj);\n"+
		"\n"+
		"	// TODO(mpl): add a line showing something like\n"+
		"	// \"Content: file (blobref)\" or\n"+
		"	// \"Content: directory (blobref)\" or\n"+
		"	// \"Content: None (has members)\".\n"+
		"\n"+
		"	// members\n"+
		"	this.reloadMembers_();\n"+
		"\n"+
		"	// TODO(mpl): use a permanent blobItemContainer instead?\n"+
		"	/* blob content */\n"+
		"	var camliContent = permObj.attr.camliContent;\n"+
		"	if (camliContent && camliContent.length > 0) {\n"+
		"		var content = goog.dom.getElement('content');\n"+
		"		content.innerHTML = '';\n"+
		"		var useFileBlobrefAsLink = \"true\";\n"+
		"		var blobItem = new camlistore.BlobItem(permanode, meta, useFileBlobrefAsLink);\n"+
		"		blobItem.decorate(content);\n"+
		"		// TODO(mpl): ideally this should be done by handleType, but it's easier\n"+
		"		// to do it now that we have a blobItem object to work with.\n"+
		"		var isdir = blobItem.getDirBlobref_()\n"+
		"		var mountTip = goog.dom.getElement(\"cammountTip\");\n"+
		"		goog.dom.removeChildren(mountTip);\n"+
		"		if (isdir != \"\") {\n"+
		"			var tip = \"Mount with:\";\n"+
		"			goog.dom.setTextContent(mountTip, tip);\n"+
		"			goog.dom.appendChild(mountTip, goog.dom.createDom(\"br\"));\n"+
		"			var codeTip = goog.dom.createDom(\"code\");\n"+
		"			goog.dom.setTextContent(codeTip, \"$ cammount /some/mountpoint \" + isdir);\n"+
		"			goog.dom.appendChild(mountTip, codeTip);\n"+
		"		}\n"+
		"	}\n"+
		"\n"+
		"	// debug attrs\n"+
		"	goog.dom.setTextContent(goog.dom.getElement(\"debugattrs\"), JSON.stringify(permOb"+
		"j.attr, null, 2));\n"+
		"\n"+
		"};\n"+
		"\n"+
		"// TODO(mpl): pass directly the permanode object\n"+
		"/**\n"+
		" * @param {string} permanode Node to describe.\n"+
		" * @param {Object} describeResult Object of properties for the node.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.PermanodePage.prototype.reloadTags_ = function(permanode, describeResu"+
		"lt) {\n"+
		"	var permanodeObject = describeResult.meta[permanode].permanode;\n"+
		"	var spanTags = document.getElementById(\"spanTags\");\n"+
		"	while (spanTags.firstChild) {\n"+
		"		spanTags.removeChild(spanTags.firstChild);\n"+
		"	}\n"+
		"	var tags = permanodeObject.attr.tag;\n"+
		"	for (idx in tags) {\n"+
		"		var tag = tags[idx];\n"+
		"\n"+
		"		var tagSpan = goog.dom.createDom(\"span\");\n"+
		"		tagSpan.className = 'cam-permanode-tag-c';\n"+
		"		var tagTextEl = goog.dom.createDom(\"span\");\n"+
		"		tagTextEl.className = 'cam-permanode-tag';\n"+
		"		goog.dom.setTextContent(tagTextEl, tag);\n"+
		"		goog.dom.appendChild(tagSpan, tagTextEl);\n"+
		"\n"+
		"		var tagDel = goog.dom.createDom(\"span\");\n"+
		"		tagDel.className = 'cam-permanode-del';\n"+
		"		goog.dom.setTextContent(tagDel, \"x\");\n"+
		"		goog.events.listen(tagDel,\n"+
		"			goog.events.EventType.CLICK,\n"+
		"			this.deleteTagFunc_(tag, tagTextEl, tagSpan),\n"+
		"			false, this\n"+
		"		);\n"+
		"\n"+
		"		goog.dom.appendChild(tagSpan, tagDel);\n"+
		"		goog.dom.appendChild(spanTags, tagSpan);\n"+
		"	}\n"+
		"}\n"+
		"\n"+
		"/**\n"+
		" * @param {Object} tag tag value to remove.\n"+
		" * @param {Object} strikeEle text element to strike while we wait for the removal"+
		" to take effect.\n"+
		" * @param {Object} removeEle element to remove.\n"+
		" * @return {Function}\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.PermanodePage.prototype.deleteTagFunc_ =\n"+
		"function(tag, strikeEle, removeEle) {\n"+
		"	var delFunc = function(e) {\n"+
		"		strikeEle.innerHTML = \"<del>\" + strikeEle.innerHTML + \"</del>\";\n"+
		"		this.connection_.newDelAttributeClaim(\n"+
		"			getPermanodeParam(),\n"+
		"			\"tag\",\n"+
		"			tag,\n"+
		"			function() {\n"+
		"				removeEle.parentNode.removeChild(removeEle);\n"+
		"			},\n"+
		"			function(msg) {\n"+
		"				alert(msg);\n"+
		"			}\n"+
		"		);\n"+
		"	};\n"+
		"	return goog.bind(delFunc, this);\n"+
		"}\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.PermanodePage.prototype.reloadMembers_ =\n"+
		"function() {\n"+
		"	var meta = this.describeResponse_.meta;\n"+
		"	var permanode = getPermanodeParam();\n"+
		"	var members = meta[permanode].permanode.attr.camliMember;\n"+
		"	var membersList = goog.dom.getElement('membersList');\n"+
		"	var membersThumbs = goog.dom.getElement('membersThumbs');\n"+
		"	membersList.innerHTML = '';\n"+
		"	if (members && members.length > 0) {\n"+
		"		var btnGallery = goog.dom.getElement('btnGallery');\n"+
		"		var doThumbnails = (btnGallery.value == \"thumbnails\");\n"+
		"		if (doThumbnails) {\n"+
		"			this.blobItemContainer_.show_();\n"+
		"		} else {\n"+
		"			this.blobItemContainer_.hide_();\n"+
		"			this.blobItemContainer_.resetChildren_();\n"+
		"		}\n"+
		"		for (idx in members) {\n"+
		"			var member = members[idx];\n"+
		"			this.addMember_(member, meta, doThumbnails);\n"+
		"		}\n"+
		"	}\n"+
		"}\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {string} pn child permanode.\n"+
		" * @param {Object} meta meta in describe response.\n"+
		" * @param {booleon} thumbnails whether to display thumbnails or a list\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.PermanodePage.prototype.addMember_ =\n"+
		"function(pn, meta, thumbnails) {\n"+
		"	var blobItem = new camlistore.BlobItem(pn, meta);\n"+
		"	if (thumbnails) {\n"+
		"		this.blobItemContainer_.addChild(blobItem, true)\n"+
		"	} else {\n"+
		"		var membersList = goog.dom.getElement(\"membersList\");\n"+
		"		var ul;\n"+
		"		if (membersList.innerHTML == \"\") {\n"+
		"			ul = goog.dom.createDom(\"ul\");\n"+
		"			goog.dom.appendChild(membersList, ul);\n"+
		"		} else {\n"+
		"			ul = membersList.firstChild;\n"+
		"		}\n"+
		"		var li = goog.dom.createDom(\"li\");\n"+
		"		var a = goog.dom.createDom(\"a\");\n"+
		"		a.href = \"./?p=\" + pn;\n"+
		"		goog.dom.setTextContent(a, blobItem.getTitle_());\n"+
		"\n"+
		"		var del = goog.dom.createDom(\"span\");\n"+
		"		del.className = 'cam-permanode-del';\n"+
		"		goog.dom.setTextContent(del, \"x\");\n"+
		"		goog.events.listen(del,\n"+
		"			goog.events.EventType.CLICK,\n"+
		"			this.deleteMemberFunc_(pn, a, li),\n"+
		"			false, this\n"+
		"		);\n"+
		"\n"+
		"		goog.dom.appendChild(li, a);\n"+
		"		goog.dom.appendChild(li, del);\n"+
		"		goog.dom.appendChild(ul, li);\n"+
		"	}\n"+
		"}\n"+
		"\n"+
		"/**\n"+
		" * @param {string} member child permanode\n"+
		" * @param {Object} strikeEle text element to strike while we wait for the removal"+
		" to take effect.\n"+
		" * @param {Object} removeEle element to remove.\n"+
		" * @return {Function}\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.PermanodePage.prototype.deleteMemberFunc_ =\n"+
		"function(member, strikeEle, removeEle) {\n"+
		"	var delFunc = function(e) {\n"+
		"		strikeEle.innerHTML = \"<del>\" + strikeEle.innerHTML + \"</del>\";\n"+
		"		this.connection_.newDelAttributeClaim(\n"+
		"			getPermanodeParam(),\n"+
		"			\"camliMember\",\n"+
		"			member,\n"+
		"			goog.bind(function() {\n"+
		"				removeEle.parentNode.removeChild(removeEle);\n"+
		"				// TODO(mpl): refreshing the whole thing is kindof heavy, maybe?\n"+
		"				this.describeBlob_();\n"+
		"			}, this),\n"+
		"			function(msg) {\n"+
		"				alert(msg);\n"+
		"			}\n"+
		"		);\n"+
		"	};\n"+
		"	return goog.bind(delFunc, this);\n"+
		"}\n"+
		"\n"+
		"/**\n"+
		" * @param {string} sourcePermanode permanode pointed by the path.\n"+
		" * @param {string} path path to remove.\n"+
		" * @param {Object} strikeEle element to remove.\n"+
		" * @return {Function}\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.PermanodePage.prototype.deletePathFunc_ =\n"+
		"function(sourcePermanode, path, strikeEle) {\n"+
		"	var delFunc = function(e) {\n"+
		"		strikeEle.innerHTML = \"<del>\" + strikeEle.innerHTML + \"</del>\";\n"+
		"		this.connection_.newDelAttributeClaim(\n"+
		"			sourcePermanode,\n"+
		"			\"camliPath:\" + path,\n"+
		"			getPermanodeParam(),\n"+
		"			goog.bind(function() {\n"+
		"				this.buildPathsList_();\n"+
		"			}, this),\n"+
		"			function(msg) {\n"+
		"				alert(msg);\n"+
		"			}\n"+
		"		);\n"+
		"	};\n"+
		"	return goog.bind(delFunc, this);\n"+
		"}\n"+
		"\n"+
		"/**\n"+
		" * @param {goog.events.Event} e The title form submit event.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.PermanodePage.prototype.handleFormTitleSubmit_ = function(e) {\n"+
		"	e.stopPropagation();\n"+
		"	e.preventDefault();\n"+
		"\n"+
		"	var inputTitle = goog.dom.getElement(\"inputTitle\");\n"+
		"	inputTitle.disabled = true;\n"+
		"	var btnSaveTitle = goog.dom.getElement(\"btnSaveTitle\");\n"+
		"	btnSaveTitle.disabled = true;\n"+
		"\n"+
		"	var startTime = new Date();\n"+
		"	this.connection_.newSetAttributeClaim(\n"+
		"		getPermanodeParam(), \"title\", inputTitle.value,\n"+
		"		goog.bind(function() {\n"+
		"			var elapsedMs = new Date().getTime() - startTime.getTime();\n"+
		"			setTimeout(goog.bind(function() {\n"+
		"				inputTitle.disabled = false;\n"+
		"				btnSaveTitle.disabled = false;\n"+
		"				this.describeBlob_();\n"+
		"			},this), Math.max(250 - elapsedMs, 0));\n"+
		"		}, this),\n"+
		"		function(msg) {\n"+
		"			alert(msg);\n"+
		"			inputTitle.disabled = false;\n"+
		"			btnSaveTitle.disabled = false;\n"+
		"		}\n"+
		"	);\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * @param {goog.events.Event} e The tags form submit event.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.PermanodePage.prototype.handleFormTagsSubmit_ = function(e) {\n"+
		"	e.stopPropagation();\n"+
		"	e.preventDefault();\n"+
		"\n"+
		"	var input = goog.dom.getElement(\"inputNewTag\");\n"+
		"	var btn = goog.dom.getElement(\"btnAddTag\");\n"+
		"	if (input.value == \"\") {\n"+
		"		return;\n"+
		"	}\n"+
		"	input.disabled = true;\n"+
		"	btn.disabled = true;\n"+
		"\n"+
		"	var startTime = new Date();\n"+
		"	var tags = input.value.split(/\\s*,\\s*/);\n"+
		"	var nRemain = tags.length;\n"+
		"	var oneDone = goog.bind(function() {\n"+
		"		nRemain--;\n"+
		"		if (nRemain == 0) {\n"+
		"			var elapsedMs = new Date().getTime() - startTime.getTime();\n"+
		"			setTimeout(goog.bind(function() {\n"+
		"				input.value = '';\n"+
		"				input.disabled = false;\n"+
		"				btn.disabled = false;\n"+
		"				this.describeBlob_();\n"+
		"			}, this), Math.max(250 - elapsedMs, 0));\n"+
		"		}\n"+
		"	}, this);\n"+
		"	for (idx in tags) {\n"+
		"		var tag = tags[idx];\n"+
		"		this.connection_.newAddAttributeClaim(\n"+
		"			getPermanodeParam(), \"tag\", tag, oneDone,\n"+
		"			function(msg) {\n"+
		"				alert(msg);\n"+
		"				oneDone();\n"+
		"			}\n"+
		"		);\n"+
		"	}\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * @param {goog.events.Event} e The access form submit event.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.PermanodePage.prototype.handleFormAccessSubmit_ = function(e) {\n"+
		"	e.stopPropagation();\n"+
		"	e.preventDefault();\n"+
		"\n"+
		"	var selectAccess = goog.dom.getElement(\"selectAccess\");\n"+
		"	selectAccess.disabled = true;\n"+
		"	var btnSaveAccess = goog.dom.getElement(\"btnSaveAccess\");\n"+
		"	btnSaveAccess.disabled = true;\n"+
		"\n"+
		"	var operation = this.connection_.newDelAttributeClaim;\n"+
		"	var value = \"\";\n"+
		"	if (selectAccess.value != \"private\") {\n"+
		"		operation = this.connection_.newSetAttributeClaim;\n"+
		"		value = selectAccess.value;\n"+
		"	}\n"+
		"\n"+
		"	var startTime = new Date();\n"+
		"	operation = goog.bind(operation, this.connection_);\n"+
		"	operation(\n"+
		"		getPermanodeParam(),\n"+
		"		\"camliAccess\",\n"+
		"		value,\n"+
		"		function() {\n"+
		"			var elapsedMs = new Date().getTime() - startTime.getTime();\n"+
		"			setTimeout(function() {\n"+
		"				selectAccess.disabled = false;\n"+
		"				btnSaveAccess.disabled = false;\n"+
		"			}, Math.max(250 - elapsedMs, 0));\n"+
		"		},\n"+
		"		function(msg) {\n"+
		"			alert(msg);\n"+
		"			selectAccess.disabled = false;\n"+
		"			btnSaveAccess.disabled = false;\n"+
		"		}\n"+
		"	);\n"+
		"\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.PermanodePage.prototype.setupRootsDropdown_ =\n"+
		"function() {\n"+
		"	var selRoots = goog.dom.getElement(\"selectPublishRoot\");\n"+
		"	if (!this.config_.publishRoots) {\n"+
		"		console.log(\"no publish roots\");\n"+
		"		return;\n"+
		"	}\n"+
		"	for (var rootName in this.config_.publishRoots) {\n"+
		"		var opt = goog.dom.createElement(\"option\");\n"+
		"		opt.setAttribute(\"value\", rootName);\n"+
		"		goog.dom.appendChild(opt,\n"+
		"			goog.dom.createTextNode(this.config_.publishRoots[rootName].prefix[0])\n"+
		"		);\n"+
		"		goog.dom.appendChild(selRoots, opt);\n"+
		"	}\n"+
		"	goog.events.listen(goog.dom.getElement(\"btnSavePublish\"),\n"+
		"		goog.events.EventType.CLICK,\n"+
		"		this.handleSavePublish_,\n"+
		"		false, this\n"+
		"	);\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {goog.events.Event} e The access form submit event.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.PermanodePage.prototype.handleSavePublish_ =\n"+
		"function(e) {\n"+
		"	var selRoots = goog.dom.getElement(\"selectPublishRoot\");\n"+
		"	var suffix = goog.dom.getElement(\"publishSuffix\");\n"+
		"\n"+
		"	var ourPermanode = getPermanodeParam();\n"+
		"	if (!ourPermanode) {\n"+
		"		return;\n"+
		"	}\n"+
		"\n"+
		"	var publishRoot = selRoots.value;\n"+
		"	if (!publishRoot) {\n"+
		"		alert(\"no publish root selected\");\n"+
		"		return;\n"+
		"	}\n"+
		"	var pathSuffix = suffix.value;\n"+
		"	if (!pathSuffix) {\n"+
		"		alert(\"no path suffix specified\");\n"+
		"		return;\n"+
		"	}\n"+
		"\n"+
		"	selRoots.disabled = true;\n"+
		"	suffix.disabled = true;\n"+
		"\n"+
		"	var enabled = function() {\n"+
		"		selRoots.disabled = false;\n"+
		"		suffix.disabled = false;\n"+
		"	};\n"+
		"\n"+
		"	// Step 1: resolve selRoots.value -> blobref of the root's permanode.\n"+
		"	// Step 2: set attribute on the root's permanode, or a sub-permanode\n"+
		"	// if multiple path components in suffix:\n"+
		"	// \"camliPath:<suffix>\" => permanode-of-ourselves\n"+
		"\n"+
		"	var sigconf = this.config_.signing;\n"+
		"	var handleFindCamliRoot = function(pnres) {\n"+
		"		if (!pnres.permanode) {\n"+
		"			alert(\"failed to publish root's permanode\");\n"+
		"			enabled();\n"+
		"			return;\n"+
		"		}\n"+
		"		var handleSetCamliPath = function() {\n"+
		"			console.log(\"success.\");\n"+
		"			enabled();\n"+
		"			selRoots.value = \"\";\n"+
		"			suffix.value = \"\";\n"+
		"			this.buildPathsList_();\n"+
		"		};\n"+
		"		var handleFailCamliPath = function() {\n"+
		"			alert(\"failed to set attribute\");\n"+
		"			enabled();\n"+
		"		};\n"+
		"		this.connection_.newSetAttributeClaim(\n"+
		"			pnres.permanode, \"camliPath:\" + pathSuffix, ourPermanode,\n"+
		"			goog.bind(handleSetCamliPath, this), handleFailCamliPath\n"+
		"		);\n"+
		"	};\n"+
		"	var handleFailFindCamliRoot = function() {\n"+
		"		alert(\"failed to find publish root's permanode\");\n"+
		"		enabled();\n"+
		"	};\n"+
		"	this.connection_.permanodeOfSignerAttrValue(\n"+
		"		sigconf.publicKeyBlobRef, \"camliRoot\", publishRoot,\n"+
		"		goog.bind(handleFindCamliRoot, this), handleFailFindCamliRoot\n"+
		"	);\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.PermanodePage.prototype.buildPathsList_ =\n"+
		"function() {\n"+
		"	var ourPermanode = getPermanodeParam();\n"+
		"	if (!ourPermanode) {\n"+
		"		return;\n"+
		"	}\n"+
		"	var sigconf = this.config_.signing;\n"+
		"\n"+
		"	var handleFindPath = function(jres) {\n"+
		"		var div = goog.dom.getElement(\"existingPaths\");\n"+
		"\n"+
		"		// TODO: there can be multiple paths in this list, but the HTML\n"+
		"		// UI only shows one.  The UI should show all, and when adding a new one\n"+
		"		// prompt users whether they want to add to or replace the existing one.\n"+
		"		// For now we just update the UI to show one.\n"+
		"		// alert(JSON.stringify(jres, null, 2));\n"+
		"		if (jres.paths && jres.paths.length > 0) {\n"+
		"			div.innerHTML = \"Existing paths for this permanode:\";\n"+
		"			var ul = goog.dom.createElement(\"ul\");\n"+
		"			goog.dom.appendChild(div,ul);\n"+
		"			for (var idx in jres.paths) {\n"+
		"				var path = jres.paths[idx];\n"+
		"				var li = goog.dom.createElement(\"li\");\n"+
		"				var span = goog.dom.createElement(\"span\");\n"+
		"				goog.dom.appendChild(li,span);\n"+
		"\n"+
		"				var blobLink = goog.dom.createElement(\"a\");\n"+
		"				blobLink.href = \".?p=\" + path.baseRef;\n"+
		"				goog.dom.setTextContent(blobLink, path.baseRef);\n"+
		"				goog.dom.appendChild(span,blobLink);\n"+
		"\n"+
		"				goog.dom.appendChild(span,goog.dom.createTextNode(\" - \"));\n"+
		"\n"+
		"				var pathLink = goog.dom.createElement(\"a\");\n"+
		"				pathLink.href = \"\";\n"+
		"				goog.dom.setTextContent(pathLink, path.suffix);\n"+
		"				for (var key in this.config_.publishRoots) {\n"+
		"					var root = this.config_.publishRoots[key];\n"+
		"					if (root.currentPermanode == path.baseRef) {\n"+
		"						// Prefix should include a trailing slash.\n"+
		"						pathLink.href = root.prefix[0] + path.suffix;\n"+
		"						// TODO: Check if we're the latest permanode\n"+
		"						// for this path and display some \"old\" notice\n"+
		"						// if not.\n"+
		"						break;\n"+
		"					}\n"+
		"				}\n"+
		"				goog.dom.appendChild(span,pathLink);\n"+
		"\n"+
		"				var del = goog.dom.createElement(\"span\");\n"+
		"				del.className = \"cam-permanode-del\";\n"+
		"				goog.dom.setTextContent(del, \"x\");\n"+
		"				goog.events.listen(del,\n"+
		"					goog.events.EventType.CLICK,\n"+
		"					this.deletePathFunc_(path.baseRef, path.suffix, span),\n"+
		"					false, this\n"+
		"				);\n"+
		"				goog.dom.appendChild(span,del);\n"+
		"\n"+
		"				goog.dom.appendChild(ul,li);\n"+
		"			}\n"+
		"		} else {\n"+
		"			div.innerHTML = \"\";\n"+
		"		}\n"+
		"	};\n"+
		"	this.connection_.pathsOfSignerTarget(sigconf.publicKeyBlobRef, ourPermanode,\n"+
		"		goog.bind(handleFindPath, this), alert\n"+
		"	);\n"+
		"};\n"+
		"\n"+
		"// TODO(mpl): reuse blobitem code for dnd?\n"+
		"/**\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.PermanodePage.prototype.setupFilesHandlers_ =\n"+
		"function() {\n"+
		"	var dnd = goog.dom.getElement(\"dnd\");\n"+
		"	goog.events.listen(goog.dom.getElement(\"fileForm\"),\n"+
		"		goog.events.EventType.SUBMIT,\n"+
		"		this.handleFilesSubmit_,\n"+
		"		false, this\n"+
		"	);\n"+
		"	goog.events.listen(goog.dom.getElement(\"fileInput\"),\n"+
		"		goog.events.EventType.CHANGE,\n"+
		"		onFileInputChange,\n"+
		"		false, this\n"+
		"	);\n"+
		"\n"+
		"	var stop = function(e) {\n"+
		"		this.classList &&\n"+
		"			goog.dom.classes.add(this, 'cam-permanode-dnd-over');\n"+
		"		e.stopPropagation();\n"+
		"		e.preventDefault();\n"+
		"	};\n"+
		"	goog.events.listen(dnd,\n"+
		"		goog.events.EventType.DRAGENTER,\n"+
		"		stop, false, this\n"+
		"	);\n"+
		"	goog.events.listen(dnd,\n"+
		"		goog.events.EventType.DRAGOVER,\n"+
		"		stop, false, this\n"+
		"	);\n"+
		"	goog.events.listen(dnd,\n"+
		"		goog.events.EventType.DRAGLEAVE,\n"+
		"		goog.bind(function() {\n"+
		"			goog.dom.classes.remove(this, 'cam-permanode-dnd-over');\n"+
		"		}, this), false, this\n"+
		"	);\n"+
		"\n"+
		"	var drop = function(e) {\n"+
		"		goog.dom.classes.remove(this, 'cam-permanode-dnd-over');\n"+
		"		stop(e);\n"+
		"		var dt = e.getBrowserEvent().dataTransfer;\n"+
		"		var files = dt.files;\n"+
		"		goog.dom.getElement(\"info\").innerHTML = \"\";\n"+
		"		this.handleFiles_(files);\n"+
		"	};\n"+
		"	goog.events.listen(dnd,\n"+
		"		goog.events.FileDropHandler.EventType.DROP,\n"+
		"		goog.bind(drop, this),\n"+
		"		false, this\n"+
		"	);\n"+
		"}\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {goog.events.Event} e The title form submit event.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.PermanodePage.prototype.handleFilesSubmit_ =\n"+
		"function(e) {\n"+
		"	e.stopPropagation();\n"+
		"	e.preventDefault();\n"+
		"	this.handleFiles_(document.getElementById(\"fileInput\").files);\n"+
		"}\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {Array<files>} files the files to upload.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.PermanodePage.prototype.handleFiles_ =\n"+
		"function(files) {\n"+
		"	for (var i = 0; i < files.length; i++) {\n"+
		"		var file = files[i];\n"+
		"		this.startFileUpload_(file);\n"+
		"	}\n"+
		"}\n"+
		"\n"+
		"/**\n"+
		" * @param {file} file the file to upload.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.PermanodePage.prototype.startFileUpload_ =\n"+
		"function(file) {\n"+
		"	var dnd = goog.dom.getElement(\"dnd\");\n"+
		"	var up = goog.dom.createElement(\"div\");\n"+
		"	up.className= 'cam-permanode-dnd-item';\n"+
		"	goog.dom.appendChild(dnd, up);\n"+
		"	var info = \"name=\" + file.name + \" size=\" + file.size + \"; type=\" + file.type;\n"+
		"\n"+
		"	var setStatus = function(status) {\n"+
		"		up.innerHTML = info + \" \" + status;\n"+
		"	};\n"+
		"	setStatus(\"(scanning)\");\n"+
		"\n"+
		"	var onFail = function(msg) {\n"+
		"		up.innerHTML = info + \" <strong>fail:</strong> \";\n"+
		"		goog.dom.appendChild(up, goog.dom.createTextNode(msg));\n"+
		"	};\n"+
		"\n"+
		"	var onGotFileSchemaRef = function(fileref) {\n"+
		"		setStatus(\" <strong>fileref: \" + fileref + \"</strong>\");\n"+
		"		this.connection_.createPermanode(\n"+
		"			goog.bind(function(filepn) {\n"+
		"				var doneWithAll = goog.bind(function() {\n"+
		"					setStatus(\"- done\");\n"+
		"					this.describeBlob_();\n"+
		"				}, this);\n"+
		"				var addMemberToParent = function() {\n"+
		"					setStatus(\"adding member\");\n"+
		"					this.connection_.newAddAttributeClaim(\n"+
		"						getPermanodeParam(), \"camliMember\", filepn,\n"+
		"						doneWithAll, onFail\n"+
		"					);\n"+
		"				};\n"+
		"				var makePermanode = goog.bind(function() {\n"+
		"					setStatus(\"making permanode\");\n"+
		"					this.connection_.newSetAttributeClaim(\n"+
		"						filepn, \"camliContent\", fileref,\n"+
		"						goog.bind(addMemberToParent, this), onFail\n"+
		"					);\n"+
		"				}, this);\n"+
		"				makePermanode();\n"+
		"			}, this),\n"+
		"			onFail\n"+
		"		);\n"+
		"	};\n"+
		"\n"+
		"	this.connection_.uploadFile(file,\n"+
		"		goog.bind(onGotFileSchemaRef, this),\n"+
		"		onFail,\n"+
		"		function(contentsRef) {\n"+
		"			setStatus(\"(checking for dup of \" + contentsRef + \")\");\n"+
		"		}\n"+
		"	);\n"+
		"}\n"+
		"\n"+
		"// Returns the first value from the query string corresponding to |key|.\n"+
		"// Returns null if the key isn't present.\n"+
		"getQueryParam = function(key) {\n"+
		"	var params = document.location.search.substring(1).split('&');\n"+
		"	for (var i = 0; i < params.length; ++i) {\n"+
		"		var parts = params[i].split('=');\n"+
		"		if (parts.length == 2 && decodeURIComponent(parts[0]) == key)\n"+
		"			return decodeURIComponent(parts[1]);\n"+
		"	}\n"+
		"	return null;\n"+
		"};\n"+
		"\n"+
		"// Returns true if the passed-in string might be a blobref.\n"+
		"isPlausibleBlobRef = function(blobRef) {\n"+
		"	return /^\\w+-[a-f0-9]+$/.test(blobRef);\n"+
		"};\n"+
		"\n"+
		"function permAttr(permanodeObject, name) {\n"+
		"	if (!(name in permanodeObject.attr)) {\n"+
		"		return null;\n"+
		"	}\n"+
		"	if (permanodeObject.attr[name].length == 0) {\n"+
		"		return null;\n"+
		"	}\n"+
		"	return permanodeObject.attr[name][0];\n"+
		"};\n"+
		"\n"+
		"function handleType(permObj) {\n"+
		"	var disablePublish = false;\n"+
		"	var selType = goog.dom.getElement(\"type\");\n"+
		"	var dnd = goog.dom.getElement(\"dnd\");\n"+
		"	var btnGallery = goog.dom.getElement(\"btnGallery\");\n"+
		"	var membersDiv = goog.dom.getElement(\"members\");\n"+
		"	dnd.style.display = \"none\";\n"+
		"	btnGallery.style.visibility = 'hidden';\n"+
		"	goog.dom.setTextContent(membersDiv, \"\");\n"+
		"	if (permAttr(permObj, \"camliRoot\")) {\n"+
		"		disablePublish = true;  // can't give a URL to a root with a claim\n"+
		"	} else if (permAttr(permObj, \"camliMember\")) {\n"+
		"		dnd.style.display = \"block\";\n"+
		"		btnGallery.style.visibility = 'visible';\n"+
		"		goog.dom.setTextContent(membersDiv, \"Members:\");\n"+
		"	}\n"+
		"\n"+
		"	goog.dom.getElement(\"selectPublishRoot\").disabled = disablePublish;\n"+
		"	goog.dom.getElement(\"publishSuffix\").disabled = disablePublish;\n"+
		"	goog.dom.getElement(\"btnSavePublish\").disabled = disablePublish;\n"+
		"};\n"+
		"\n"+
		"\n"+
		"function $(id) { return goog.dom.getElement(id) }\n"+
		"\n"+
		"function onFileInputChange(e) {\n"+
		"    var s = \"\";\n"+
		"    var files = $(\"fileInput\").files;\n"+
		"    for (var i = 0; i < files.length; i++) {\n"+
		"        var file = files[i];\n"+
		"        s += \"<p>\" + file.name + \"</p>\";\n"+
		"    }\n"+
		"    var fl = $(\"filelist\");\n"+
		"    fl.innerHTML = s;\n"+
		"}\n"+
		"\n"+
		""))
}
