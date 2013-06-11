// THIS FILE IS AUTO-GENERATED FROM search.js
// DO NOT EDIT.

package newui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("search.js", 10137, time.Unix(0, 1370942742232957700), fileembed.String("/**\n"+
		" * @fileoverview Entry point for the permanodes search UI.\n"+
		" *\n"+
		" */\n"+
		"goog.provide('camlistore.SearchPage');\n"+
		"\n"+
		"goog.require('goog.array');\n"+
		"goog.require('goog.dom');\n"+
		"goog.require('goog.dom.classes');\n"+
		"goog.require('goog.events.EventHandler');\n"+
		"goog.require('goog.events.EventType');\n"+
		"goog.require('goog.ui.Component');\n"+
		"goog.require('camlistore.BlobItemContainer');\n"+
		"goog.require('camlistore.ServerConnection');\n"+
		"goog.require('camlistore.Toolbar');\n"+
		"goog.require('camlistore.Toolbar.EventType');\n"+
		"\n"+
		"\n"+
		"// TODO(mpl): better help. tooltip maybe?\n"+
		"\n"+
		"// TODO(mpl): make a mother class that both index.js and search.js could\n"+
		"// inherit from?\n"+
		"/**\n"+
		" * @param {camlistore.ServerType.DiscoveryDocument} config Global config\n"+
		" *	 of the current server this page is being rendered for.\n"+
		" * @param {goog.dom.DomHelper=} opt_domHelper DOM helper to use.\n"+
		" *\n"+
		" * @extends {goog.ui.Component}\n"+
		" * @constructor\n"+
		" */\n"+
		"camlistore.SearchPage = function(config, opt_domHelper) {\n"+
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
		"	/**\n"+
		"	 * @type {camlistore.BlobItemContainer}\n"+
		"	 * @private\n"+
		"	 */\n"+
		"	this.blobItemContainer_ = new camlistore.BlobItemContainer(\n"+
		"		this.connection_, opt_domHelper);\n"+
		"\n"+
		"	/**\n"+
		"	 * @type {camlistore.Toolbar}\n"+
		"	 * @private\n"+
		"	 */\n"+
		"	this.toolbar_ = new camlistore.Toolbar(opt_domHelper);\n"+
		"	this.toolbar_.isSearch = true;\n"+
		"\n"+
		"	/**\n"+
		"	 * @type {goog.events.EventHandler}\n"+
		"	 * @private\n"+
		"	 */\n"+
		"	this.eh_ = new goog.events.EventHandler(this);\n"+
		"};\n"+
		"goog.inherits(camlistore.SearchPage, goog.ui.Component);\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @enum {string}\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.SearchPage.prototype.searchPrefix_ = {\n"+
		"  TAG: 'tag:',\n"+
		"  TITLE: 'title:',\n"+
		"  BLOBREF: 'bref:'\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @type {number}\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.SearchPage.prototype.maxInResponse_ = 100;\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * Creates an initial DOM representation for the component.\n"+
		" */\n"+
		"camlistore.SearchPage.prototype.createDom = function() {\n"+
		"	this.decorateInternal(this.dom_.createElement('div'));\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * Decorates an existing HTML DIV element.\n"+
		" * @param {Element} element The DIV element to decorate.\n"+
		" */\n"+
		"camlistore.SearchPage.prototype.decorateInternal = function(element) {\n"+
		"	camlistore.SearchPage.superClass_.decorateInternal.call(this, element);\n"+
		"\n"+
		"	var el = this.getElement();\n"+
		"	goog.dom.classes.add(el, 'cam-index-page');\n"+
		"\n"+
		"	var titleEl = this.dom_.createDom('h1', 'cam-index-page-title');\n"+
		"	this.dom_.setTextContent(titleEl, \"Search\");\n"+
		"	this.dom_.appendChild(el, titleEl);\n"+
		"\n"+
		"	this.addChild(this.toolbar_, true);\n"+
		"\n"+
		"	var searchForm = this.dom_.createDom('form', {'id': 'searchForm'});\n"+
		"	var searchText = this.dom_.createDom('input',\n"+
		"		{'type': 'text', 'id': 'searchText', 'size': 50, 'title': 'Search'}\n"+
		"	);\n"+
		"	var btnSearch = this.dom_.createDom('input',\n"+
		"		{'type': 'submit', 'id': 'btnSearch', 'value': 'Search'}\n"+
		"	);\n"+
		"	goog.dom.appendChild(searchForm, searchText);\n"+
		"	goog.dom.appendChild(searchForm, btnSearch);\n"+
		"	goog.dom.appendChild(el, searchForm);\n"+
		"	\n"+
		"	this.addChild(this.blobItemContainer_, true);\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/** @override */\n"+
		"camlistore.SearchPage.prototype.disposeInternal = function() {\n"+
		"	camlistore.SearchPage.superClass_.disposeInternal.call(this);\n"+
		"	this.eh_.dispose();\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * Called when component's element is known to be in the document.\n"+
		" */\n"+
		"camlistore.SearchPage.prototype.enterDocument = function() {\n"+
		"	camlistore.SearchPage.superClass_.enterDocument.call(this);\n"+
		"\n"+
		"	this.eh_.listen(\n"+
		"		this.toolbar_, camlistore.Toolbar.EventType.BIGGER,\n"+
		"		function() {\n"+
		"			this.blobItemContainer_.bigger();\n"+
		"		}\n"+
		"	);\n"+
		"\n"+
		"	this.eh_.listen(\n"+
		"		this.toolbar_, camlistore.Toolbar.EventType.SMALLER,\n"+
		"		function() {\n"+
		"			this.blobItemContainer_.smaller();\n"+
		"		}\n"+
		"	);\n"+
		"\n"+
		"	this.eh_.listen(\n"+
		"		this.toolbar_, camlistore.Toolbar.EventType.ROOTS,\n"+
		"		function() {\n"+
		"			this.blobItemContainer_.showRoots(this.config_.signing);\n"+
		"		}\n"+
		"	);\n"+
		"\n"+
		"	this.eh_.listen(\n"+
		"		this.toolbar_, camlistore.Toolbar.EventType.HOME,\n"+
		"		function() {\n"+
		"			window.open('./index.html', 'Home');\n"+
		"		}\n"+
		"	);\n"+
		"\n"+
		"	this.eh_.listen(\n"+
		"		goog.dom.getElement('searchForm'),\n"+
		"		goog.events.EventType.SUBMIT,\n"+
		"		this.handleTextSearch_\n"+
		"	);\n"+
		"\n"+
		"	this.eh_.listen(\n"+
		"		this.toolbar_, camlistore.Toolbar.EventType.CHECKED_ITEMS_CREATE_SET,\n"+
		"		function() {\n"+
		"			var blobItems = this.blobItemContainer_.getCheckedBlobItems();\n"+
		"			this.createNewSetWithItems_(blobItems);\n"+
		"		}\n"+
		"	);\n"+
		"\n"+
		"	this.eh_.listen(\n"+
		"		this.toolbar_, camlistore.Toolbar.EventType.CHECKED_ITEMS_ADDTO_SET,\n"+
		"		function() {\n"+
		"			var blobItems = this.blobItemContainer_.getCheckedBlobItems();\n"+
		"			this.addItemsToSet_(blobItems);\n"+
		"		}\n"+
		"	);\n"+
		"\n"+
		"	this.eh_.listen(\n"+
		"		this.blobItemContainer_,\n"+
		"		camlistore.BlobItemContainer.EventType.BLOB_ITEMS_CHOSEN,\n"+
		"		function() {\n"+
		"			this.handleItemSelection_(false);\n"+
		"		}\n"+
		"	);\n"+
		"\n"+
		"	this.eh_.listen(\n"+
		"		this.blobItemContainer_,\n"+
		"		camlistore.BlobItemContainer.EventType.SINGLE_NODE_CHOSEN,\n"+
		"		function() {\n"+
		"			this.handleItemSelection_(true);\n"+
		"		}\n"+
		"	);\n"+
		"\n"+
		"	this.eh_.listen(\n"+
		"		this.toolbar_, camlistore.Toolbar.EventType.SELECT_COLLEC,\n"+
		"		function() {\n"+
		"			var blobItems = this.blobItemContainer_.getCheckedBlobItems();\n"+
		"			// there should be only one item selected\n"+
		"			if (blobItems.length != 1) {\n"+
		"				alert(\"Select (only) one item to set as the default collection.\");\n"+
		"				return;\n"+
		"			}\n"+
		"			this.blobItemContainer_.currentCollec_ = blobItems[0].blobRef_;\n"+
		"			this.blobItemContainer_.unselectAll();\n"+
		"			this.toolbar_.setCheckedBlobItemCount(0);\n"+
		"			this.toolbar_.toggleCollecButton(false);\n"+
		"			this.toolbar_.toggleAddToSetButton(false);\n"+
		"		}\n"+
		"	);\n"+
		"\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {boolean} single Whether a single item has been (un)selected.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.SearchPage.prototype.handleItemSelection_ =\n"+
		"function(single) {\n"+
		"	var blobItems = this.blobItemContainer_.getCheckedBlobItems();\n"+
		"	this.toolbar_.setCheckedBlobItemCount(blobItems.length);\n"+
		"	// set checkedItemsAddToSetButton_\n"+
		"	if (this.blobItemContainer_.currentCollec_ &&\n"+
		"		this.blobItemContainer_.currentCollec_ != \"\" &&\n"+
		"		blobItems.length > 0) {\n"+
		"		this.toolbar_.toggleAddToSetButton(true);\n"+
		"	} else {\n"+
		"		this.toolbar_.toggleAddToSetButton(false);\n"+
		"	}\n"+
		"	// set setAsCollecButton_\n"+
		"	if (single &&\n"+
		"		blobItems.length == 1 &&\n"+
		"		blobItems[0].isCollection()) {\n"+
		"		this.toolbar_.toggleCollecButton(true);\n"+
		"	} else {\n"+
		"		this.toolbar_.toggleCollecButton(false);\n"+
		"	}\n"+
		"}\n"+
		"\n"+
		"// Returns true if the passed-in string might be a blobref.\n"+
		"isPlausibleBlobRef = function(blobRef) {\n"+
		"	return /^\\w+-[a-f0-9]+$/.test(blobRef);\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * @param {goog.events.Event} e The title form submit event.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.SearchPage.prototype.handleTextSearch_ =\n"+
		"function(e) {\n"+
		"	e.stopPropagation();\n"+
		"	e.preventDefault();\n"+
		"\n"+
		"	var searchText = goog.dom.getElement(\"searchText\");\n"+
		"	searchText.disabled = true;\n"+
		"	var btnSearch = goog.dom.getElement(\"btnSearch\");\n"+
		"	btnSearch.disabled = true;\n"+
		"\n"+
		"	var attr = \"\";\n"+
		"	var value = \"\";\n"+
		"	var fuzzy = false;\n"+
		"	if (searchText.value.indexOf(this.searchPrefix_.TAG) == 0) {\n"+
		"		// search by tag\n"+
		"		attr = \"tag\";\n"+
		"		value = searchText.value.slice(this.searchPrefix_.TAG.length);\n"+
		"		// TODO(mpl): allow fuzzy option for tag search. How?\n"+
		"		// \":fuzzy\" at the end of search string maybe?\n"+
		"	} else if (searchText.value.indexOf(this.searchPrefix_.TITLE) == 0) {\n"+
		"		// search by title\n"+
		"		attr = \"title\";\n"+
		"		value = searchText.value.slice(this.searchPrefix_.TITLE.length);\n"+
		"		// TODO(mpl): fuzzy search seems to be broken for title. investigate.\n"+
		"	} else if (searchText.value.indexOf(this.searchPrefix_.BLOBREF) == 0) {\n"+
		"		// or query directly by blobref (useful to get a permanode and set it\n"+
		"		// as the default collection)\n"+
		"		value = searchText.value.slice(this.searchPrefix_.BLOBREF.length);\n"+
		"		if (isPlausibleBlobRef(value)) {\n"+
		"			this.blobItemContainer_.findByBlobref_(value);\n"+
		"		}\n"+
		"		searchText.disabled = false;\n"+
		"		btnSearch.disabled = false;\n"+
		"		return;\n"+
		"	} else {\n"+
		"		attr = \"\";\n"+
		"		value = searchText.value;\n"+
		"		fuzzy = true;\n"+
		"	}\n"+
		"\n"+
		"	this.blobItemContainer_.showWithAttr(this.config_.signing,\n"+
		"		attr, value, fuzzy, this.maxInResponse_\n"+
		"	);\n"+
		"	searchText.disabled = false;\n"+
		"	btnSearch.disabled = false;\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * Called when component's element is known to have been removed from the\n"+
		" * document.\n"+
		" */\n"+
		"camlistore.SearchPage.prototype.exitDocument = function() {\n"+
		"	camlistore.SearchPage.superClass_.exitDocument.call(this);\n"+
		"	this.eh_.dispose();\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {Array.<camlistore.BlobItem>} blobItems Items to add to the permanode.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.SearchPage.prototype.createNewSetWithItems_ = function(blobItems) {\n"+
		"	this.connection_.createPermanode(\n"+
		"		goog.bind(this.addMembers_, this, true, blobItems));\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * @param {Array.<camlistore.BlobItem>} blobItems Items to add to the permanode.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.SearchPage.prototype.addItemsToSet_ = function(blobItems) {\n"+
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
		"camlistore.SearchPage.prototype.addMembers_ =\n"+
		"function(newSet, blobItems, permanode) {\n"+
		"	var deferredList = [];\n"+
		"	var complete = goog.bind(this.addItemsToSetDone_, this, permanode);\n"+
		"	var callback = function() {\n"+
		"		deferredList.push(1);\n"+
		"		if (deferredList.length == blobItems.length) {\n"+
		"			complete();\n"+
		"		}\n"+
		"	};\n"+
		"\n"+
		"	// TODO(mpl): newSet is a lame trick. Do better.\n"+
		"	if (newSet) {\n"+
		"		this.connection_.newSetAttributeClaim(\n"+
		"			permanode, 'title', 'My new set', function() {}\n"+
		"		);\n"+
		"	}\n"+
		"	goog.array.forEach(blobItems, function(blobItem, index) {\n"+
		"		this.connection_.newAddAttributeClaim(\n"+
		"			permanode, 'camliMember', blobItem.getBlobRef(), callback\n"+
		"		);\n"+
		"	}, this);\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {string} permanode Node to which the items were added.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.SearchPage.prototype.addItemsToSetDone_ = function(permanode) {\n"+
		"	this.blobItemContainer_.unselectAll();\n"+
		"	var blobItems = this.blobItemContainer_.getCheckedBlobItems();\n"+
		"	this.toolbar_.setCheckedBlobItemCount(blobItems.length);\n"+
		"	this.toolbar_.toggleCollecButton(false);\n"+
		"	this.toolbar_.toggleAddToSetButton(false);\n"+
		"};\n"+
		""))
}
