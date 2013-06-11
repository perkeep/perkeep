// THIS FILE IS AUTO-GENERATED FROM blobinfo.js
// DO NOT EDIT.

package newui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("blobinfo.js", 6086, time.Unix(0, 1370942742232957700), fileembed.String("/*\n"+
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
		" * @fileoverview Blob description page.\n"+
		" *\n"+
		" */\n"+
		"goog.provide('camlistore.BlobPage');\n"+
		"\n"+
		"goog.require('goog.dom');\n"+
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
		"camlistore.BlobPage = function(config, opt_domHelper) {\n"+
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
		"};\n"+
		"goog.inherits(camlistore.BlobPage, goog.ui.Component);\n"+
		"\n"+
		"/**\n"+
		" * @type {number}\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.BlobPage.prototype.thumbnailSize_ = 200;\n"+
		"\n"+
		"/**\n"+
		" * Called when component's element is known to be in the document.\n"+
		" */\n"+
		"camlistore.BlobPage.prototype.enterDocument = function() {\n"+
		"	var blobref = getBlobParam();\n"+
		"	if (!blobref) {\n"+
		"		alert(\"missing blob param in url\");\n"+
		"		return;\n"+
		"	}\n"+
		"	var blobmeta = goog.dom.getElement('blobmeta');\n"+
		"	blobmeta.innerText = \"(loading)\";\n"+
		"\n"+
		"	var blobdescribe = goog.dom.getElement('blobdescribe');\n"+
		"	blobdescribe.innerHTML = \"<a href='\" +\n"+
		"		goog.uri.utils.appendPath(this.config_.searchRoot,\n"+
		"			'camli/search/describe?blobref=' + blobref) +\n"+
		"		 \"'>describe</a>\";\n"+
		"	this.describeBlob_(blobref);\n"+
		"\n"+
		"}\n"+
		"\n"+
		"/**\n"+
		" * @param {string} blobRef BlobRef for the uploaded file.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.BlobPage.prototype.describeBlob_ =\n"+
		"function(blobRef) {\n"+
		"	this.connection_.describeWithThumbnails(\n"+
		"		blobRef,\n"+
		"		0,\n"+
		"		goog.bind(this.handleDescribeBlob_, this),\n"+
		"		function(msg) {\n"+
		"			alert(\"Error describing blob \" + blobRef + \": \" + msg);\n"+
		"		}\n"+
		"	);\n"+
		"};\n"+
		"\n"+
		"\n"+
		"// TODO(mpl): improve blob_item and redo the following based on it.\n"+
		"/**\n"+
		" * @param {Object} bmap Describe request response.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.BlobPage.prototype.handleDescribeBlob_ =\n"+
		"function(bmap) {\n"+
		"	var blobmeta = goog.dom.getElement('blobmeta');\n"+
		"	var bd = goog.dom.getElement(\"blobdownload\");\n"+
		"	bd.innerHTML = \"\";\n"+
		"	var blobref = getBlobParam();\n"+
		"	if (!blobref) {\n"+
		"		alert(\"no blobref?\");\n"+
		"		return;\n"+
		"	}\n"+
		"	var binfo = bmap.meta[blobref];\n"+
		"	if (!binfo) {\n"+
		"		blobmeta.innerHTML = \"(not found)\";\n"+
		"		return;\n"+
		"	}\n"+
		"	blobmeta.innerHTML = JSON.stringify(binfo, null, 2);\n"+
		"	if (binfo.camliType || (binfo.type && binfo.type.indexOf(\"text/\") == 0)) {\n"+
		"		this.connection_.getBlobContents(blobref,\n"+
		"			goog.bind(function(data) {\n"+
		"				goog.dom.getElement(\"blobdata\").innerHTML = linkifyBlobRefs(data);\n"+
		"				var bb = goog.dom.getElement('blobbrowse');\n"+
		"				if (binfo.camliType != \"directory\") {\n"+
		"					bb.style.visibility = 'hidden';\n"+
		"				} else {\n"+
		"					bb.innerHTML = \"<a href='?d=\" + blobref + \"'>browse</a>\";\n"+
		"				}\n"+
		"				if (binfo.camliType == \"file\") {\n"+
		"					// TODO(mpl): we can't get the thumnails url in a describe\n"+
		"					// response because the server only gives it for a permanode.\n"+
		"					//  That's why we do this messy business here. Fix it server side.\n"+
		"					try {\n"+
		"						finfo = JSON.parse(data);\n"+
		"						bd.innerHTML = \"<a href=''></a>\";\n"+
		"						var fileName = finfo.fileName || blobref;\n"+
		"						bd.firstChild.href = \"./download/\" + blobref + \"/\" + fileName;\n"+
		"						if (binfo.file.mimeType.indexOf(\"image/\") == 0) {\n"+
		"							var thumbURL = \"<img src='./thumbnail/\" + blobref + \"/\" +\n"+
		"								fileName + \"?mw=\" + this.thumbnailSize_ +\n"+
		"								\"&mh=\" + this.thumbnailSize_ + \"'>\";\n"+
		"							goog.dom.getElement(\"thumbnail\").innerHTML = thumbURL;\n"+
		"						} else {\n"+
		"							goog.dom.getElement(\"thumbnail\").innerHTML = \"\";\n"+
		"						}\n"+
		"						goog.dom.setTextContent(bd.firstChild, fileName);\n"+
		"						bd.innerHTML = \"download: \" + bd.innerHTML;\n"+
		"					} catch (x) {\n"+
		"						// TOD(mpl): why?\n"+
		"					}\n"+
		"				}\n"+
		"			}, this),\n"+
		"			alert\n"+
		"		);\n"+
		"	} else {\n"+
		"		goog.dom.getElement(\"blobdata\").innerHTML = \"<em>Unknown/binary data</em>\";\n"+
		"	}\n"+
		"	bd.innerHTML = \"<a href='\" +\n"+
		"		goog.uri.utils.appendPath(\n"+
		"			this.config_.blobRoot,\n"+
		"			\"camli/\" + blobref)\n"+
		"		+ \"'>download</a>\";\n"+
		"\n"+
		"	if (binfo.camliType && binfo.camliType == \"permanode\") {\n"+
		"		goog.dom.getElement(\"editspan\").style.display = \"inline\";\n"+
		"		goog.dom.getElement(\"editlink\").href = \"./?p=\" + blobref;\n"+
		"\n"+
		"		var claims = goog.dom.getElement(\"claimsdiv\");\n"+
		"		claims.style.visibility = \"\";\n"+
		"		this.connection_.permanodeClaims(blobref,\n"+
		"			function(data) {\n"+
		"				goog.dom.getElement(\"claims\").innerHTML = linkifyBlobRefs(JSON.stringify(data"+
		", null, 2));\n"+
		"			},\n"+
		"			function(msg) {\n"+
		"				alert(msg);\n"+
		"			}\n"+
		"		);\n"+
		"	}\n"+
		"}\n"+
		"\n"+
		"function linkifyBlobRefs(schemaBlob) {\n"+
		"	var re = /(\\w{3,6}-[a-f0-9]{30,})/g;\n"+
		"	return schemaBlob.replace(re, \"<a href='./?b=$1'>$1</a>\");\n"+
		"};\n"+
		"\n"+
		"// Gets the |p| query parameter, assuming that it looks like a blobref.\n"+
		"function getBlobParam() {\n"+
		"	var blobRef = getQueryParam('b');\n"+
		"	return (blobRef && isPlausibleBlobRef(blobRef)) ? blobRef : null;\n"+
		"}\n"+
		"\n"+
		"// TODO(mpl): move it to a common place (used by permanode.js too).\n"+
		"// I suppose we could go back to depending on camli.js for these little helpers o"+
		"nly.\n"+
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
		""))
}
