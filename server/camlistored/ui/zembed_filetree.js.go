// THIS FILE IS AUTO-GENERATED FROM filetree.js
// DO NOT EDIT.

package newui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("filetree.js", 7346, time.Unix(0, 1370942742232957700), fileembed.String("/*\n"+
		"Copyright 2011 The Camlistore Authors.\n"+
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
		"/**\n"+
		" * @fileoverview Filetree page.\n"+
		" *\n"+
		" */\n"+
		"goog.provide('camlistore.FiletreePage');\n"+
		"\n"+
		"goog.require('goog.dom');\n"+
		"goog.require('goog.events.EventType');\n"+
		"goog.require('goog.ui.Component');\n"+
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
		"camlistore.FiletreePage = function(config, opt_domHelper) {\n"+
		"	goog.base(this, opt_domHelper);\n"+
		"\n"+
		"	/**\n"+
		"	 * @type {Object}\n"+
		"	 * @private\n"+
		"	 */\n"+
		"	this.config_ = config;\n"+
		"\n"+
		"	/**\n"+
		"	 * @type {camlistore.ServerConnection}\n"+
		"	 * @private\n"+
		"	 */\n"+
		"	this.connection_ = new camlistore.ServerConnection(config);\n"+
		"\n"+
		"};\n"+
		"goog.inherits(camlistore.FiletreePage, goog.ui.Component);\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @type {number}\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.FiletreePage.prototype.indentStep_ = 20;\n"+
		"\n"+
		"\n"+
		"function getDirBlobrefParam() {\n"+
		"	var blobRef = getQueryParam('d');\n"+
		"	return (blobRef && isPlausibleBlobRef(blobRef)) ? blobRef : null;\n"+
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
		"\n"+
		"/**\n"+
		" * Called when component's element is known to be in the document.\n"+
		" */\n"+
		"camlistore.FiletreePage.prototype.enterDocument = function() {\n"+
		"	camlistore.FiletreePage.superClass_.enterDocument.call(this);\n"+
		"	var blobref = getDirBlobrefParam();\n"+
		"\n"+
		"	if (blobref) {\n"+
		"		this.connection_.describeWithThumbnails(\n"+
		"			blobref,\n"+
		"			0,\n"+
		"			goog.bind(this.handleDescribeBlob_, this, blobref),\n"+
		"			function(msg) {\n"+
		"				alert(\"failed to get blob description: \" + msg);\n"+
		"			}\n"+
		"		);\n"+
		"	}\n"+
		"}\n"+
		"\n"+
		"/**\n"+
		" * @param {string} blobref blob to describe.\n"+
		" * @param {camlistore.ServerType.DescribeResponse} describeResult Object of prope"+
		"rties for the node.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.FiletreePage.prototype.handleDescribeBlob_ =\n"+
		"function(blobref, describeResult) {\n"+
		"	var meta = describeResult.meta;\n"+
		"	if (!meta[blobref]) {\n"+
		"		alert(\"didn't get blob \" + blobref);\n"+
		"		return;\n"+
		"	}\n"+
		"	var binfo = meta[blobref];\n"+
		"	if (!binfo) {\n"+
		"		alert(\"Error describing blob \" + blobref);\n"+
		"		return;\n"+
		"	}\n"+
		"	if (binfo.camliType != \"directory\") {\n"+
		"		alert(\"Does not contain a directory\");\n"+
		"		return;\n"+
		"	}\n"+
		"	this.connection_.getBlobContents(\n"+
		"		blobref,\n"+
		"		goog.bind(function(data) {\n"+
		"			var finfo = JSON.parse(data);\n"+
		"			var fileName = finfo.fileName;\n"+
		"			var curDir = document.getElementById('curDir');\n"+
		"			curDir.innerHTML = \"<a href='./?b=\" + blobref + \"'>\" + fileName + \"</a>\";\n"+
		"			this.buildTree_();\n"+
		"		}, this),\n"+
		"		function(msg) {\n"+
		"			alert(\"failed to get blobcontents: \" + msg);\n"+
		"		}\n"+
		"	);\n"+
		"}\n"+
		"\n"+
		"/**\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.FiletreePage.prototype.buildTree_ = function() {\n"+
		"	var blobref = getDirBlobrefParam();\n"+
		"	var children = goog.dom.getElement(\"children\");\n"+
		"	this.connection_.getFileTree(blobref,\n"+
		"		goog.bind(function(jres) {\n"+
		"			this.onChildrenFound_(children, 0, jres);\n"+
		"		}, this)\n"+
		"	);\n"+
		"}\n"+
		"\n"+
		"/**\n"+
		" * @param {string} div node used as root for the tree\n"+
		" * @param {number} depth how deep we are in the tree, for indenting\n"+
		" * @param {camlistore.ServerType.DescribeResponse} jres describe result\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.FiletreePage.prototype.onChildrenFound_ =\n"+
		"function(div, depth, jres) {\n"+
		"	var indent = depth * camlistore.FiletreePage.prototype.indentStep_;\n"+
		"	div.innerHTML = \"\";\n"+
		"	for (var i = 0; i < jres.children.length; i++) {\n"+
		"		var children = jres.children;\n"+
		"		var pdiv = goog.dom.createElement(\"div\");\n"+
		"		var alink = goog.dom.createElement(\"a\");\n"+
		"		alink.style.paddingLeft=indent + \"px\"\n"+
		"		alink.id = children[i].blobRef;\n"+
		"		switch (children[i].type) {\n"+
		"		case 'directory':\n"+
		"			goog.dom.setTextContent(alink, \"+ \" + children[i].name);\n"+
		"			goog.events.listen(alink,\n"+
		"				goog.events.EventType.CLICK,\n"+
		"				goog.bind(function (b, d) {\n"+
		"					this.unFold_(b, d);\n"+
		"				}, this, alink.id, depth),\n"+
		"				false, this\n"+
		"			);\n"+
		"			break;\n"+
		"		case 'file':\n"+
		"			goog.dom.setTextContent(alink, \"  \" + children[i].name);\n"+
		"			alink.href = \"./?b=\" + alink.id;\n"+
		"			break;\n"+
		"		default:\n"+
		"			alert(\"not a file or dir\");\n"+
		"			break;\n"+
		"		}\n"+
		"		var newPerm = goog.dom.createElement(\"span\");\n"+
		"		newPerm.className = \"cam-filetree-newp\";\n"+
		"		goog.dom.setTextContent(newPerm, \"P\");\n"+
		"		goog.events.listen(newPerm,\n"+
		"			goog.events.EventType.CLICK,\n"+
		"			this.newPermWithContent_(alink.id),\n"+
		"			false, this\n"+
		"		);\n"+
		"		goog.dom.appendChild(pdiv, alink);\n"+
		"		goog.dom.appendChild(pdiv, newPerm);\n"+
		"		goog.dom.appendChild(div, pdiv);\n"+
		"	}\n"+
		"}\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {string} content blobref of the content\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.FiletreePage.prototype.newPermWithContent_ =\n"+
		"function(content) {\n"+
		"	var fun = function(e) {\n"+
		"		this.connection_.createPermanode(\n"+
		"			goog.bind(function(permanode) {\n"+
		"				this.connection_.newAddAttributeClaim(\n"+
		"					permanode, \"camliContent\", content,\n"+
		"					function() {\n"+
		"						alert(\"permanode created\");\n"+
		"					},\n"+
		"					function(msg) {\n"+
		"						// TODO(mpl): \"cancel\" new permanode\n"+
		"						alert(\"set permanode content failed: \" + msg);\n"+
		"					}\n"+
		"				);\n"+
		"			}, this),\n"+
		"			function(msg) {\n"+
		"				alert(\"create permanode failed: \" + msg);\n"+
		"			}\n"+
		"		);\n"+
		"	}\n"+
		"	return goog.bind(fun, this);\n"+
		"}\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {string} blobref dir to unfold.\n"+
		" * @param {number} depth so we know how much to indent.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.FiletreePage.prototype.unFold_ =\n"+
		"function(blobref, depth) {\n"+
		"	var node = goog.dom.getElement(blobref);\n"+
		"	var div = goog.dom.createElement(\"div\");\n"+
		"	this.connection_.getFileTree(blobref,\n"+
		"		goog.bind(function(jres) {\n"+
		"			this.onChildrenFound_(div, depth+1, jres);\n"+
		"			insertAfter(node, div);\n"+
		"			goog.events.removeAll(node);\n"+
		"			goog.events.listen(node,\n"+
		"				goog.events.EventType.CLICK,\n"+
		"				goog.bind(function(b, d) {\n"+
		"					this.fold_(b, d);\n"+
		"				}, this, blobref, depth),\n"+
		"				false, this\n"+
		"			);\n"+
		"		}, this)\n"+
		"	);\n"+
		"}\n"+
		"\n"+
		"function insertAfter( referenceNode, newNode ) {\n"+
		"	// nextSibling X2 because of the \"P\" span\n"+
		"	referenceNode.parentNode.insertBefore( newNode, referenceNode.nextSibling.nextSi"+
		"bling );\n"+
		"}\n"+
		"\n"+
		"/**\n"+
		" * @param {string} nodeid id of the node to fold.\n"+
		" * @param {depth} depth so we know how much to indent.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.FiletreePage.prototype.fold_ =\n"+
		"function(nodeid, depth) {\n"+
		"	var node = goog.dom.getElement(nodeid);\n"+
		"	// nextSibling X2 because of the \"P\" span\n"+
		"	node.parentNode.removeChild(node.nextSibling.nextSibling);\n"+
		"	goog.events.removeAll(node);\n"+
		"	goog.events.listen(node,\n"+
		"		goog.events.EventType.CLICK,\n"+
		"		goog.bind(function(b, d) {\n"+
		"			this.unFold_(b, d);\n"+
		"		}, this, nodeid, depth),\n"+
		"		false, this\n"+
		"	);\n"+
		"}\n"+
		"\n"+
		""))
}
