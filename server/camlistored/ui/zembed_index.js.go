// THIS FILE IS AUTO-GENERATED FROM index.js
// DO NOT EDIT.

package newui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("index.js", 9095, time.Unix(0, 1370942742232957700), fileembed.String("/**\n"+
		" * @fileoverview Entry point for the blob browser UI.\n"+
		" *\n"+
		" */\n"+
		"goog.provide('camlistore.IndexPage');\n"+
		"\n"+
		"goog.require('goog.array');\n"+
		"goog.require('goog.dom');\n"+
		"goog.require('goog.dom.classes');\n"+
		"goog.require('goog.events.EventHandler');\n"+
		"goog.require('goog.events.EventType');\n"+
		"goog.require('goog.ui.Component');\n"+
		"goog.require('goog.ui.Textarea');\n"+
		"goog.require('camlistore.BlobItemContainer');\n"+
		"goog.require('camlistore.ServerConnection');\n"+
		"goog.require('camlistore.Toolbar');\n"+
		"goog.require('camlistore.Toolbar.EventType');\n"+
		"goog.require('camlistore.ServerType');\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {camlistore.ServerType.DiscoveryDocument} config Global config\n"+
		" *   of the current server this page is being rendered for.\n"+
		" * @param {goog.dom.DomHelper=} opt_domHelper DOM helper to use.\n"+
		" *\n"+
		" * @extends {goog.ui.Component}\n"+
		" * @constructor\n"+
		" */\n"+
		"camlistore.IndexPage = function(config, opt_domHelper) {\n"+
		"  goog.base(this, opt_domHelper);\n"+
		"\n"+
		"  /**\n"+
		"   * @type {Object}\n"+
		"   * @private\n"+
		"   */\n"+
		"  this.config_ = config;\n"+
		"\n"+
		"  /**\n"+
		"   * @type {camlistore.ServerConnection}\n"+
		"   * @private\n"+
		"   */\n"+
		"  this.connection_ = new camlistore.ServerConnection(config);\n"+
		"\n"+
		"  /**\n"+
		"   * @type {camlistore.BlobItemContainer}\n"+
		"   * @private\n"+
		"   */\n"+
		"  this.blobItemContainer_ = new camlistore.BlobItemContainer(\n"+
		"      this.connection_, opt_domHelper);\n"+
		"  this.blobItemContainer_.setHasCreateItem(true);\n"+
		"\n"+
		"  /**\n"+
		"   * @type {Element}\n"+
		"   * @private\n"+
		"   */\n"+
		"  this.serverInfo_;\n"+
		"\n"+
		"  /**\n"+
		"   * @type {camlistore.Toolbar}\n"+
		"   * @private\n"+
		"   */\n"+
		"  this.toolbar_ = new camlistore.Toolbar(opt_domHelper);\n"+
		"\n"+
		"  /**\n"+
		"   * @type {goog.events.EventHandler}\n"+
		"   * @private\n"+
		"   */\n"+
		"  this.eh_ = new goog.events.EventHandler(this);\n"+
		"};\n"+
		"goog.inherits(camlistore.IndexPage, goog.ui.Component);\n"+
		"\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * Creates an initial DOM representation for the component.\n"+
		" */\n"+
		"camlistore.IndexPage.prototype.createDom = function() {\n"+
		"  this.decorateInternal(this.dom_.createElement('div'));\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * Decorates an existing HTML DIV element.\n"+
		" * @param {Element} element The DIV element to decorate.\n"+
		" */\n"+
		"camlistore.IndexPage.prototype.decorateInternal = function(element) {\n"+
		"  camlistore.IndexPage.superClass_.decorateInternal.call(this, element);\n"+
		"\n"+
		"  var el = this.getElement();\n"+
		"  goog.dom.classes.add(el, 'cam-index-page');\n"+
		"\n"+
		"  var titleEl = this.dom_.createDom('h1', 'cam-index-title');\n"+
		"  this.dom_.setTextContent(titleEl, this.config_.ownerName + '\\'s Vault');\n"+
		"  this.dom_.appendChild(el, titleEl);\n"+
		"\n"+
		"  this.serverInfo_ = this.dom_.createDom('div', 'cam-index-serverinfo');\n"+
		"  this.dom_.appendChild(el, this.serverInfo_);\n"+
		"\n"+
		"  this.addChild(this.toolbar_, true);\n"+
		"  this.addChild(this.blobItemContainer_, true);\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/** @override */\n"+
		"camlistore.IndexPage.prototype.disposeInternal = function() {\n"+
		"  camlistore.IndexPage.superClass_.disposeInternal.call(this);\n"+
		"  this.eh_.dispose();\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * Called when component's element is known to be in the document.\n"+
		" */\n"+
		"camlistore.IndexPage.prototype.enterDocument = function() {\n"+
		"  camlistore.IndexPage.superClass_.enterDocument.call(this);\n"+
		"\n"+
		"	this.connection_.serverStatus(\n"+
		"		goog.bind(function(resp) {\n"+
		"			this.handleServerStatus_(resp);\n"+
		"		}, this)\n"+
		"	);\n"+
		"\n"+
		"  this.eh_.listen(\n"+
		"      this.toolbar_, camlistore.Toolbar.EventType.BIGGER,\n"+
		"      function() {\n"+
		"        if (this.blobItemContainer_.bigger()) {\n"+
		"          this.blobItemContainer_.showRecent();\n"+
		"        }\n"+
		"      });\n"+
		"\n"+
		"  this.eh_.listen(\n"+
		"      this.toolbar_, camlistore.Toolbar.EventType.SMALLER,\n"+
		"      function() {\n"+
		"        if (this.blobItemContainer_.smaller()) {\n"+
		"          this.blobItemContainer_.showRecent();\n"+
		"        }\n"+
		"      });\n"+
		"\n"+
		"  this.eh_.listen(\n"+
		"      this.toolbar_, camlistore.Toolbar.EventType.GOSEARCH,\n"+
		"      function() {\n"+
		"        window.open('./search.html', 'Search');\n"+
		"      });\n"+
		"\n"+
		"  this.eh_.listen(\n"+
		"      this.toolbar_, camlistore.Toolbar.EventType.CHECKED_ITEMS_CREATE_SET,\n"+
		"      function() {\n"+
		"        var blobItems = this.blobItemContainer_.getCheckedBlobItems();\n"+
		"        this.createNewSetWithItems_(blobItems);\n"+
		"      });\n"+
		"\n"+
		"  this.eh_.listen(\n"+
		"      this.toolbar_, camlistore.Toolbar.EventType.CHECKED_ITEMS_ADDTO_SET,\n"+
		"      function() {\n"+
		"        var blobItems = this.blobItemContainer_.getCheckedBlobItems();\n"+
		"        this.addItemsToSet_(blobItems);\n"+
		"      });\n"+
		"\n"+
		"  this.eh_.listen(\n"+
		"      this.toolbar_, camlistore.Toolbar.EventType.SELECT_COLLEC,\n"+
		"      function() {\n"+
		"        var blobItems = this.blobItemContainer_.getCheckedBlobItems();\n"+
		"        // there should be only one item selected\n"+
		"        if (blobItems.length != 1) {\n"+
		"          alert(\"Cannet set multiple items as current collection\");\n"+
		"          return;\n"+
		"        }\n"+
		"        this.blobItemContainer_.currentCollec_ = blobItems[0].blobRef_;\n"+
		"        this.blobItemContainer_.unselectAll();\n"+
		"        this.toolbar_.setCheckedBlobItemCount(0);\n"+
		"        this.toolbar_.toggleCollecButton(false);\n"+
		"        this.toolbar_.toggleAddToSetButton(false);\n"+
		"      });\n"+
		"\n"+
		"  // TODO(mpl): those are getting large. make dedicated funcs.\n"+
		"  this.eh_.listen(\n"+
		"      this.blobItemContainer_,\n"+
		"      camlistore.BlobItemContainer.EventType.BLOB_ITEMS_CHOSEN,\n"+
		"      function() {\n"+
		"        var blobItems = this.blobItemContainer_.getCheckedBlobItems();\n"+
		"        this.toolbar_.setCheckedBlobItemCount(blobItems.length);\n"+
		"        // set checkedItemsAddToSetButton_\n"+
		"        if (this.blobItemContainer_.currentCollec_ &&\n"+
		"          this.blobItemContainer_.currentCollec_ != \"\" &&\n"+
		"          blobItems.length > 0) {\n"+
		"          this.toolbar_.toggleAddToSetButton(true);\n"+
		"        } else {\n"+
		"          this.toolbar_.toggleAddToSetButton(false);\n"+
		"        }\n"+
		"        // set setAsCollecButton_\n"+
		"        this.toolbar_.toggleCollecButton(false);\n"+
		"      });\n"+
		"\n"+
		"  this.eh_.listen(\n"+
		"      this.blobItemContainer_,\n"+
		"      camlistore.BlobItemContainer.EventType.SINGLE_NODE_CHOSEN,\n"+
		"      function() {\n"+
		"        var blobItems = this.blobItemContainer_.getCheckedBlobItems();\n"+
		"        this.toolbar_.setCheckedBlobItemCount(blobItems.length);\n"+
		"        // set checkedItemsAddToSetButton_\n"+
		"        if (this.blobItemContainer_.currentCollec_ &&\n"+
		"          this.blobItemContainer_.currentCollec_ != \"\" &&\n"+
		"          blobItems.length > 0) {\n"+
		"          this.toolbar_.toggleAddToSetButton(true);\n"+
		"        } else {\n"+
		"          this.toolbar_.toggleAddToSetButton(false);\n"+
		"        }\n"+
		"        // set setAsCollecButton_\n"+
		"        if (blobItems.length == 1 &&\n"+
		"          blobItems[0].isCollection()) {\n"+
		"          this.toolbar_.toggleCollecButton(true);\n"+
		"        } else {\n"+
		"          this.toolbar_.toggleCollecButton(false);\n"+
		"        }\n"+
		"      });\n"+
		"\n"+
		"  this.blobItemContainer_.showRecent();\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * Called when component's element is known to have been removed from the\n"+
		" * document.\n"+
		" */\n"+
		"camlistore.IndexPage.prototype.exitDocument = function() {\n"+
		"  camlistore.IndexPage.superClass_.exitDocument.call(this);\n"+
		"  // Clear event handlers here\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {Array.<camlistore.BlobItem>} blobItems Items to add to the permanode.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.IndexPage.prototype.createNewSetWithItems_ = function(blobItems) {\n"+
		"  this.connection_.createPermanode(\n"+
		"      goog.bind(this.addMembers_, this, true, blobItems));\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * @param {Array.<camlistore.BlobItem>} blobItems Items to add to the permanode.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.IndexPage.prototype.addItemsToSet_ = function(blobItems) {\n"+
		"	if (!this.blobItemContainer_.currentCollec_ ||\n"+
		"		this.blobItemContainer_.currentCollec_ == \"\") {\n"+
		"		alert(\"no destination collection selected\");\n"+
		"	}\n"+
		"	this.addMembers_(false, blobItems, this.blobItemContainer_.currentCollec_);\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * @param {boolean} newSet Whether the containing set has just been created.\n"+
		" * @param {Array.<camlistore.BlobItem>} blobItems Items to add to the permanode.\n"+
		" * @param {string} permanode Node to add the items to.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.IndexPage.prototype.addMembers_ =\n"+
		"    function(newSet, blobItems, permanode) {\n"+
		"  var deferredList = [];\n"+
		"  var complete = goog.bind(this.addItemsToSetDone_, this, permanode);\n"+
		"  var callback = function() {\n"+
		"    deferredList.push(1);\n"+
		"    if (deferredList.length == blobItems.length) {\n"+
		"      complete();\n"+
		"    }\n"+
		"  };\n"+
		"\n"+
		"  // TODO(mpl): newSet is a lame trick. Do better.\n"+
		"  if (newSet) {\n"+
		"    this.connection_.newSetAttributeClaim(\n"+
		"      permanode, 'title', 'My new set', function() {}\n"+
		"    );\n"+
		"  }\n"+
		"  goog.array.forEach(blobItems, function(blobItem, index) {\n"+
		"    this.connection_.newAddAttributeClaim(\n"+
		"        permanode, 'camliMember', blobItem.getBlobRef(), callback);\n"+
		"  }, this);\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {string} permanode Node to which the items were added.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.IndexPage.prototype.addItemsToSetDone_ = function(permanode) {\n"+
		"  this.blobItemContainer_.unselectAll();\n"+
		"  this.toolbar_.setCheckedBlobItemCount(0);\n"+
		"  this.toolbar_.toggleCollecButton(false);\n"+
		"  this.toolbar_.toggleAddToSetButton(false);\n"+
		"  this.blobItemContainer_.showRecent();\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * @param {camlistore.ServerType.StatusResponse} resp response for a status reque"+
		"st\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.IndexPage.prototype.handleServerStatus_ =\n"+
		"function(resp) {\n"+
		"	if (resp == null) {\n"+
		"		return;\n"+
		"	}\n"+
		"	goog.dom.removeChildren(this.serverInfo_);\n"+
		"	if (resp.version) {\n"+
		"		var version = \"Camlistore version: \" + resp.version + \"\\n\";\n"+
		"		var div = this.dom_.createDom('div');\n"+
		"		goog.dom.setTextContent(div, version);\n"+
		"		goog.dom.appendChild(this.serverInfo_, div);\n"+
		"	}\n"+
		"};\n"+
		"\n"+
		""))
}
