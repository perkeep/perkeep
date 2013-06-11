// THIS FILE IS AUTO-GENERATED FROM pics.js
// DO NOT EDIT.

package newui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("pics.js", 5250, time.Unix(0, 1370942742232957700), fileembed.String("/*\n"+
		"Copyright 2013 The Camlistore Authors.\n"+
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
		" * @fileoverview Pictures gallery page.\n"+
		" *\n"+
		" */\n"+
		"goog.provide('camlistore.GalleryPage');\n"+
		"\n"+
		"goog.require('goog.dom');\n"+
		"goog.require('goog.events.EventHandler');\n"+
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
		"camlistore.GalleryPage = function(config, opt_domHelper) {\n"+
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
		"goog.inherits(camlistore.GalleryPage, goog.ui.Component);\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * Decorates an existing HTML DIV element.\n"+
		" * @param {Element} element The DIV element to decorate.\n"+
		" */\n"+
		"camlistore.GalleryPage.prototype.decorateInternal = function(element) {\n"+
		"	camlistore.GalleryPage.superClass_.decorateInternal.call(this, element);\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/** @override */\n"+
		"camlistore.GalleryPage.prototype.disposeInternal = function() {\n"+
		"	camlistore.GalleryPage.superClass_.disposeInternal.call(this);\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * Called when component's element is known to be in the document.\n"+
		" */\n"+
		"camlistore.GalleryPage.prototype.enterDocument = function() {\n"+
		"	camlistore.GalleryPage.superClass_.enterDocument.call(this);\n"+
		"\n"+
		"	var members = goog.dom.getElement('members');\n"+
		"	if (!members) {\n"+
		"		return;\n"+
		"	}\n"+
		"	var children = goog.dom.getChildren(members);\n"+
		"	if (!children || children.length < 1) {\n"+
		"		return;\n"+
		"	}\n"+
		"	goog.array.forEach(children, function(li) {\n"+
		"		li.src = li.src + '&square=1';\n"+
		"	})\n"+
		"\n"+
		"	if (camliViewIsOwner) {\n"+
		"		var el = this.getElement();\n"+
		"		goog.dom.classes.add(el, 'camliadmin');\n"+
		"\n"+
		"		goog.array.forEach(children, function(li) {\n"+
		"		var lichild = goog.dom.getFirstElementChild(li);\n"+
		"		var titleSpan = goog.dom.getNextElementSibling(goog.dom.getFirstElementChild(li"+
		"child));\n"+
		"		var editLink = goog.dom.createElement('a', {'href': '#'});\n"+
		"		goog.dom.classes.add(editLink, 'hidden');\n"+
		"		goog.dom.setTextContent(editLink, 'edit title');\n"+
		"\n"+
		"		var titleInput = goog.dom.createElement('input');\n"+
		"		goog.dom.classes.add(titleInput, 'hidden');\n"+
		"\n"+
		"		goog.events.listen(editLink,\n"+
		"			goog.events.EventType.CLICK,\n"+
		"			function(e) {\n"+
		"				goog.dom.classes.remove(titleSpan, 'visible');\n"+
		"				goog.dom.classes.add(titleSpan, 'hidden');\n"+
		"				goog.dom.classes.remove(titleInput, 'hidden');\n"+
		"				goog.dom.classes.add(titleInput, 'visible');\n"+
		"				titleInput.focus();\n"+
		"				titleInput.select();\n"+
		"				e.stopPropagation();\n"+
		"				e.preventDefault();\n"+
		"			},\n"+
		"			false, this\n"+
		"		);\n"+
		"		goog.events.listen(li,\n"+
		"			goog.events.EventType.MOUSEOVER,\n"+
		"				function(e) {\n"+
		"					goog.dom.classes.remove(editLink, 'hidden');\n"+
		"					goog.dom.classes.add(editLink, 'pics-edit');\n"+
		"				},\n"+
		"				false, editLink\n"+
		"		);\n"+
		"		goog.events.listen(li,\n"+
		"			goog.events.EventType.MOUSEOUT,\n"+
		"				function(e) {\n"+
		"					goog.dom.classes.remove(editLink, 'pics-edit');\n"+
		"					goog.dom.classes.add(editLink, 'hidden');\n"+
		"					goog.dom.classes.remove(titleInput, 'visible');\n"+
		"					goog.dom.classes.add(titleInput, 'hidden');\n"+
		"					goog.dom.classes.remove(titleSpan, 'hidden');\n"+
		"					goog.dom.classes.add(titleSpan, 'visible');\n"+
		"				},\n"+
		"				false, editLink\n"+
		"		);\n"+
		"		goog.events.listen(titleInput,\n"+
		"			goog.events.EventType.KEYPRESS,\n"+
		"			goog.bind(function(e) {\n"+
		"				if (e.keyCode == 13) {\n"+
		"					this.saveImgTitle_(titleInput, titleSpan);\n"+
		"				}\n"+
		"			}, this),\n"+
		"			false, this\n"+
		"		);\n"+
		"		goog.dom.insertChildAt(lichild, editLink, 1);\n"+
		"		goog.dom.insertChildAt(li, titleInput, 1);\n"+
		"		}, this\n"+
		"		)\n"+
		"	}\n"+
		"}\n"+
		"\n"+
		"/**\n"+
		" * @param {string} titleInput text field element for title\n"+
		" * @param {string} titleSpan span element containing the title\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.GalleryPage.prototype.saveImgTitle_ =\n"+
		"function (titleInput, titleSpan) {\n"+
		"	var spanText = goog.dom.getTextContent(titleSpan);\n"+
		"	var newVal = titleInput.value;\n"+
		"	if (newVal != \"\" && newVal != spanText) {\n"+
		"		goog.dom.setTextContent(titleSpan, newVal);\n"+
		"		var blobRef = goog.dom.getParentElement(titleInput).id.replace(/^camli-/, '');\n"+
		"		this.connection_.newSetAttributeClaim(\n"+
		"			blobRef,\n"+
		"			\"title\",\n"+
		"			newVal,\n"+
		"			function() {\n"+
		"			},\n"+
		"			function(msg) {\n"+
		"				alert(msg);\n"+
		"			}\n"+
		"		);\n"+
		"	}\n"+
		"	goog.dom.classes.remove(titleInput, 'visible');\n"+
		"	goog.dom.classes.add(titleInput, 'hidden');\n"+
		"	goog.dom.classes.remove(titleSpan, 'hidden');\n"+
		"	goog.dom.classes.add(titleSpan, 'visible');\n"+
		"}\n"+
		"\n"+
		"/**\n"+
		" * Called when component's element is known to have been removed from the\n"+
		" * document.\n"+
		" */\n"+
		"camlistore.GalleryPage.prototype.exitDocument = function() {\n"+
		"	camlistore.GalleryPage.superClass_.exitDocument.call(this);\n"+
		"};\n"+
		"\n"+
		""))
}
