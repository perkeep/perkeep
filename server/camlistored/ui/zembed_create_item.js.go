// THIS FILE IS AUTO-GENERATED FROM create_item.js
// DO NOT EDIT.

package newui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("create_item.js", 2234, time.Unix(0, 1370942742232957700), fileembed.String("/**\n"+
		" * @fileoverview Placeholder BlobItem in a BlobItemContainer that lets the\n"+
		" * user upload new blobs to the server.\n"+
		" *\n"+
		" */\n"+
		"goog.provide('camlistore.CreateItem');\n"+
		"\n"+
		"goog.require('goog.dom');\n"+
		"goog.require('goog.dom.classes');\n"+
		"goog.require('goog.events.EventHandler');\n"+
		"goog.require('goog.events.EventType');\n"+
		"goog.require('goog.ui.Control');\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {goog.dom.DomHelper=} opt_domHelper DOM helper to use.\n"+
		" *\n"+
		" * @extends {goog.ui.Control}\n"+
		" * @constructor\n"+
		" */\n"+
		"camlistore.CreateItem = function(opt_domHelper) {\n"+
		"  goog.base(this, opt_domHelper);\n"+
		"\n"+
		"  /**\n"+
		"   * @type {goog.events.EventHandler}\n"+
		"   * @private\n"+
		"   */\n"+
		"  this.eh_ = new goog.events.EventHandler(this);\n"+
		"};\n"+
		"goog.inherits(camlistore.CreateItem, goog.ui.Control);\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * Creates an initial DOM representation for the component.\n"+
		" */\n"+
		"camlistore.CreateItem.prototype.createDom = function() {\n"+
		"  this.decorateInternal(this.dom_.createElement('div'));\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * Decorates an existing HTML DIV element.\n"+
		" * @param {Element} element The DIV element to decorate.\n"+
		" */\n"+
		"camlistore.CreateItem.prototype.decorateInternal = function(element) {\n"+
		"  camlistore.CreateItem.superClass_.decorateInternal.call(this, element);\n"+
		"\n"+
		"  var el = this.getElement();\n"+
		"  goog.dom.classes.add(el, 'cam-blobitem', 'cam-createitem');\n"+
		"\n"+
		"  var plusEl = this.dom_.createDom('a', 'cam-createitem-link');\n"+
		"  plusEl.href = 'javascript:void(0)';\n"+
		"  this.dom_.setTextContent(plusEl, '+')\n"+
		"  this.dom_.appendChild(el, plusEl);\n"+
		"\n"+
		"  var titleEl = this.dom_.createDom('p', 'cam-createitem-thumbtitle');\n"+
		"  this.dom_.setTextContent(titleEl, 'Drag & drop files or click');\n"+
		"  this.dom_.appendChild(el, titleEl);\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/** @override */\n"+
		"camlistore.CreateItem.prototype.disposeInternal = function() {\n"+
		"  camlistore.CreateItem.superClass_.disposeInternal.call(this);\n"+
		"  this.eh_.dispose();\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * Called when component's element is known to be in the document.\n"+
		" */\n"+
		"camlistore.CreateItem.prototype.enterDocument = function() {\n"+
		"  camlistore.CreateItem.superClass_.enterDocument.call(this);\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * Called when component's element is known to have been removed from the\n"+
		" * document.\n"+
		" */\n"+
		"camlistore.CreateItem.prototype.exitDocument = function() {\n"+
		"  camlistore.CreateItem.superClass_.exitDocument.call(this);\n"+
		"  this.eh_.removeAll();\n"+
		"};\n"+
		""))
}
