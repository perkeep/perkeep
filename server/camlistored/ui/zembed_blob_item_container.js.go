// THIS FILE IS AUTO-GENERATED FROM blob_item_container.js
// DO NOT EDIT.

package newui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("blob_item_container.js", 18189, time.Unix(0, 1371078083685270484), fileembed.String("/**\n"+
		" * @fileoverview Contains a set of BlobItems. Knows how to fetch items from\n"+
		" * the server side. Is preconfigured with common queries like \"recent\" blobs.\n"+
		" *\n"+
		" */\n"+
		"goog.provide('camlistore.BlobItemContainer');\n"+
		"\n"+
		"goog.require('goog.dom');\n"+
		"goog.require('goog.dom.classes');\n"+
		"goog.require('goog.events.Event');\n"+
		"goog.require('goog.events.EventHandler');\n"+
		"goog.require('goog.events.EventType');\n"+
		"goog.require('goog.events.FileDropHandler');\n"+
		"goog.require('goog.ui.Container');\n"+
		"goog.require('camlistore.BlobItem');\n"+
		"goog.require('camlistore.CreateItem');\n"+
		"goog.require('camlistore.ServerConnection');\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {camlistore.ServerConnection} connection Connection to the server\n"+
		" *   for fetching blobrefs and other queries.\n"+
		" * @param {goog.dom.DomHelper=} opt_domHelper DOM helper to use.\n"+
		" *\n"+
		" * @extends {goog.ui.Container}\n"+
		" * @constructor\n"+
		" */\n"+
		"camlistore.BlobItemContainer = function(connection, opt_domHelper) {\n"+
		"  goog.base(this, opt_domHelper);\n"+
		"\n"+
		"  /**\n"+
		"   * @type {Array.<camlistore.BlobItem>}\n"+
		"   * @private\n"+
		"   */\n"+
		"  this.checkedBlobItems_ = [];\n"+
		"\n"+
		"  /**\n"+
		"   * @type {camlistore.ServerConnection}\n"+
		"   * @private\n"+
		"   */\n"+
		"  this.connection_ = connection;\n"+
		"\n"+
		"  /**\n"+
		"   * BlobRef of the permanode defined as the current collection/set.\n"+
		"   * Selected blobitems will be added as members of that collection\n"+
		"   * upon relevant actions (e.g click on the 'Add to Set' toolbar button).\n"+
		"   * @type {string}\n"+
		"   * @private\n"+
		"   */\n"+
		"  this.currentCollec_ = \"\";\n"+
		"\n"+
		"  /**\n"+
		"   * @type {goog.events.EventHandler}\n"+
		"   * @private\n"+
		"   */\n"+
		"  this.eh_ = new goog.events.EventHandler(this);\n"+
		"};\n"+
		"goog.inherits(camlistore.BlobItemContainer, goog.ui.Container);\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @type {Array.<number>}\n"+
		" */\n"+
		"camlistore.BlobItemContainer.THUMBNAIL_SIZES_ = [25, 50, 75, 100, 150, 200];\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @type {goog.events.FileDropHandler}\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.fileDropHandler_ = null;\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @type {Element}\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.dragActiveElement_ = null;\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @type {number}\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.dragDepth_ = 0;\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * Constants for events fired by BlobItemContainer\n"+
		" * @enum {string}\n"+
		" */\n"+
		"camlistore.BlobItemContainer.EventType = {\n"+
		"  BLOB_ITEMS_CHOSEN: 'Camlistore_BlobItemContainer_BlobItems_Chosen',\n"+
		"  SINGLE_NODE_CHOSEN: 'Camlistore_BlobItemContainer_SingleNode_Chosen'\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @type {number}\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.thumbnailSize_ = 100;\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @type {boolean}\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.hasCreateItem_ = false;\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @return {boolean}\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.smaller = function() {\n"+
		"  var index = camlistore.BlobItemContainer.THUMBNAIL_SIZES_.indexOf(\n"+
		"      this.thumbnailSize_);\n"+
		"  if (index == 0) {\n"+
		"    return false;\n"+
		"  }\n"+
		"  var el = this.getElement();\n"+
		"  goog.dom.classes.remove(el, 'cam-blobitemcontainer-' + this.thumbnailSize_);\n"+
		"  this.thumbnailSize_ = camlistore.BlobItemContainer.THUMBNAIL_SIZES_[index-1];\n"+
		"  goog.dom.classes.add(el, 'cam-blobitemcontainer-' + this.thumbnailSize_);\n"+
		"  return true;\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @return {boolean}\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.bigger = function() {\n"+
		"  var index = camlistore.BlobItemContainer.THUMBNAIL_SIZES_.indexOf(\n"+
		"      this.thumbnailSize_);\n"+
		"  if (index == camlistore.BlobItemContainer.THUMBNAIL_SIZES_.length - 1) {\n"+
		"    return false;\n"+
		"  }\n"+
		"  var el = this.getElement();\n"+
		"  goog.dom.classes.remove(el, 'cam-blobitemcontainer-' + this.thumbnailSize_);\n"+
		"  this.thumbnailSize_ = camlistore.BlobItemContainer.THUMBNAIL_SIZES_[index+1];\n"+
		"  goog.dom.classes.add(el, 'cam-blobitemcontainer-' + this.thumbnailSize_);\n"+
		"  return true;\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {boolean} v\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.setHasCreateItem = function(v) {\n"+
		"  this.hasCreateItem_ = v;\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * Creates an initial DOM representation for the component.\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.createDom = function() {\n"+
		"  this.decorateInternal(this.dom_.createElement('div'));\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * Decorates an existing HTML DIV element.\n"+
		" * @param {Element} element The DIV element to decorate.\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.decorateInternal = function(element) {\n"+
		"  camlistore.BlobItemContainer.superClass_.decorateInternal.call(this, element);\n"+
		"\n"+
		"  var el = this.getElement();\n"+
		"  goog.dom.classes.add(el, 'cam-blobitemcontainer');\n"+
		"  goog.dom.classes.add(el, 'cam-blobitemcontainer-' + this.thumbnailSize_);\n"+
		"\n"+
		"  var dropMessageEl = this.dom_.createDom(\n"+
		"      'div', 'cam-blobitemcontainer-drag-message',\n"+
		"      'Drag & drop item to upload.');\n"+
		"  var dropIndicatorEl = this.dom_.createDom(\n"+
		"      'div', 'cam-blobitemcontainer-drag-indicator');\n"+
		"  this.dom_.appendChild(dropIndicatorEl, dropMessageEl);\n"+
		"  this.dom_.appendChild(el, dropIndicatorEl);\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/** @override */\n"+
		"camlistore.BlobItemContainer.prototype.disposeInternal = function() {\n"+
		"  camlistore.BlobItemContainer.superClass_.disposeInternal.call(this);\n"+
		"  this.eh_.dispose();\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * Called when component's element is known to be in the document.\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.enterDocument = function() {\n"+
		"  camlistore.BlobItemContainer.superClass_.enterDocument.call(this);\n"+
		"\n"+
		"  this.resetChildren_();\n"+
		"  this.listenToBlobItemEvents_();\n"+
		"\n"+
		"  this.fileDropHandler_ = new goog.events.FileDropHandler(\n"+
		"      this.getElement());\n"+
		"  this.registerDisposable(this.fileDropHandler_);\n"+
		"  this.eh_.listen(\n"+
		"      this.fileDropHandler_,\n"+
		"      goog.events.FileDropHandler.EventType.DROP,\n"+
		"      this.handleFileDrop_);\n"+
		"  this.eh_.listen(\n"+
		"      this.getElement(),\n"+
		"      goog.events.EventType.DRAGENTER,\n"+
		"      this.handleFileDragEnter_);\n"+
		"  this.eh_.listen(\n"+
		"      this.getElement(),\n"+
		"      goog.events.EventType.DRAGLEAVE,\n"+
		"      this.handleFileDragLeave_);\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * Called when component's element is known to have been removed from the\n"+
		" * document.\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.exitDocument = function() {\n"+
		"  camlistore.BlobItemContainer.superClass_.exitDocument.call(this);\n"+
		"  this.eh_.removeAll();\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * Show recent blobs.\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.showRecent = function() {\n"+
		"  this.connection_.getRecentlyUpdatedPermanodes(\n"+
		"      goog.bind(this.showRecentDone_, this),\n"+
		"      this.thumbnailSize_);\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * Show roots\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.showRoots =\n"+
		"function(sigconf) {\n"+
		"	this.connection_.permanodesWithAttr(sigconf.publicKeyBlobRef,\n"+
		"		\"camliRoot\", \"\", false, 0, this.thumbnailSize_,\n"+
		"		goog.bind(this.showWithAttrDone_, this),\n"+
		"		function(msg) {\n"+
		"			alert(msg);\n"+
		"		}\n"+
		"	);\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * Search and show permanodes matching the specified criteria.\n"+
		" * @param {string} sigconf\n"+
		" * @param {string} attr\n"+
		" * @param {string} value\n"+
		" * @param {boolean} fuzzy Noop because not supported yet.\n"+
		" * @param {number} max max number of items in response.\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.showWithAttr =\n"+
		"function(sigconf, attr, value, fuzzy, max) {\n"+
		"	this.connection_.permanodesWithAttr(sigconf.publicKeyBlobRef,\n"+
		"		attr, value, fuzzy, max, this.thumbnailSize_,\n"+
		"		goog.bind(this.showWithAttrDone_, this),\n"+
		"		function(msg) {\n"+
		"			alert(msg);\n"+
		"		}\n"+
		"	);\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * Search for a permanode with the required blobref\n"+
		" * @param {string} blobref\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.findByBlobref_ =\n"+
		"function(blobref) {\n"+
		"	this.connection_.describeWithThumbnails(\n"+
		"		blobref,\n"+
		"		this.thumbnailSize_,\n"+
		"		goog.bind(this.findByBlobrefDone_, this, blobref),\n"+
		"		function(msg) {\n"+
		"			alert(msg);\n"+
		"		}\n"+
		"	);\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * @return {Array.<camlistore.BlobItem>}\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.getCheckedBlobItems = function() {\n"+
		"  return this.checkedBlobItems_;\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * Subscribes to events dispatched by blob items.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.listenToBlobItemEvents_ = function() {\n"+
		"  var doc = goog.dom.getOwnerDocument(this.element_);\n"+
		"  this.eh_.\n"+
		"      listen(this, goog.ui.Component.EventType.CHECK,\n"+
		"             this.handleBlobItemChecked_).\n"+
		"      listen(this, goog.ui.Component.EventType.UNCHECK,\n"+
		"             this.handleBlobItemChecked_).\n"+
		"      listen(doc,\n"+
		"             goog.events.EventType.KEYDOWN,\n"+
		"             this.handleKeyDownEvent_).\n"+
		"      listen(doc,\n"+
		"             goog.events.EventType.KEYUP,\n"+
		"             this.handleKeyUpEvent_);\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @type {boolean}\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.isShiftKeyDown_ = false;\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @type {boolean}\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.isCtrlKeyDown_ = false;\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * Sets state for whether or not the shift or ctrl key is down.\n"+
		" * @param {goog.events.KeyEvent} e A key event.\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.handleKeyDownEvent_ = function(e) {\n"+
		"  if (e.keyCode == goog.events.KeyCodes.SHIFT) {\n"+
		"    this.isShiftKeyDown_ = true;\n"+
		"    this.isCtrlKeyDown_ = false;\n"+
		"    return;\n"+
		"  }\n"+
		"  if (e.keyCode == goog.events.KeyCodes.CTRL) {\n"+
		"    this.isCtrlKeyDown_ = true;\n"+
		"    this.isShiftKeyDown_ = false;\n"+
		"    return;\n"+
		"  }\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * Sets state for whether or not the shift or ctrl key is up.\n"+
		" * @param {goog.events.KeyEvent} e A key event.\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.handleKeyUpEvent_ = function(e) {\n"+
		"  this.isShiftKeyDown_ = false;\n"+
		"  this.isCtrlKeyDown_ = false;\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {goog.events.Event} e An event.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.handleBlobItemChecked_ = function(e) {\n"+
		"  // Because the CHECK/UNCHECK event dispatches before isChecked is set.\n"+
		"  // We stop the default behaviour because want to control manually here whether\n"+
		"  // the source blobitem gets checked or not. See http://camlistore.org/issue/134\n"+
		"  e.preventDefault();\n"+
		"  var blobItem = e.target;\n"+
		"  var isCheckingItem = !blobItem.isChecked();\n"+
		"  var isShiftMultiSelect = this.isShiftKeyDown_;\n"+
		"  var isCtrlMultiSelect = this.isCtrlKeyDown_;\n"+
		"\n"+
		"  if (isShiftMultiSelect || isCtrlMultiSelect) {\n"+
		"    var lastChildSelected =\n"+
		"        this.checkedBlobItems_[this.checkedBlobItems_.length - 1];\n"+
		"    var firstChildSelected =\n"+
		"        this.checkedBlobItems_[0];\n"+
		"    var lastChosenIndex = this.indexOfChild(lastChildSelected);\n"+
		"    var firstChosenIndex = this.indexOfChild(firstChildSelected);\n"+
		"    var thisIndex = this.indexOfChild(blobItem);\n"+
		"  }\n"+
		"\n"+
		"  if (isShiftMultiSelect) {\n"+
		"    // deselect all items after the chosen one\n"+
		"    for (var i = lastChosenIndex; i > thisIndex; i--) {\n"+
		"      var item = this.getChildAt(i);\n"+
		"      item.setState(goog.ui.Component.State.CHECKED, false);\n"+
		"      if (goog.array.contains(this.checkedBlobItems_, item)) {\n"+
		"        goog.array.remove(this.checkedBlobItems_, item);\n"+
		"      }\n"+
		"    }\n"+
		"    // make sure all the others are selected.\n"+
		"    for (var i = firstChosenIndex; i <= thisIndex; i++) {\n"+
		"      var item = this.getChildAt(i);\n"+
		"      item.setState(goog.ui.Component.State.CHECKED, true);\n"+
		"      if (!goog.array.contains(this.checkedBlobItems_, item)) {\n"+
		"        this.checkedBlobItems_.push(item);\n"+
		"      }\n"+
		"    }\n"+
		"    this.dispatchEvent(camlistore.BlobItemContainer.EventType.BLOB_ITEMS_CHOSEN);\n"+
		"  } else if (isCtrlMultiSelect) {\n"+
		"    if (isCheckingItem) {\n"+
		"      blobItem.setState(goog.ui.Component.State.CHECKED, true);\n"+
		"      if (!goog.array.contains(this.checkedBlobItems_, blobItem)) {\n"+
		"        var pos = -1;\n"+
		"        for (var i = 0; i <= this.checkedBlobItems_.length; i++) {\n"+
		"          var idx = this.indexOfChild(this.checkedBlobItems_[i]);\n"+
		"          if (idx > thisIndex) {\n"+
		"            pos = i;\n"+
		"            break;\n"+
		"          }\n"+
		"        }\n"+
		"        if (pos != -1) {\n"+
		"          goog.array.insertAt(this.checkedBlobItems_, blobItem, pos)\n"+
		"        } else {\n"+
		"          this.checkedBlobItems_.push(blobItem);\n"+
		"        }\n"+
		"      }\n"+
		"    } else {\n"+
		"      blobItem.setState(goog.ui.Component.State.CHECKED, false);\n"+
		"      if (goog.array.contains(this.checkedBlobItems_, blobItem)) {\n"+
		"        var done = goog.array.remove(this.checkedBlobItems_, blobItem);\n"+
		"        if (!done) {\n"+
		"          alert(\"Failed to remove item from selection\");\n"+
		"        }\n"+
		"      }\n"+
		"    }\n"+
		"    this.dispatchEvent(camlistore.BlobItemContainer.EventType.BLOB_ITEMS_CHOSEN);\n"+
		"  } else {\n"+
		"    // unselect all chosen items.\n"+
		"    goog.array.forEach(this.checkedBlobItems_, function(item) {\n"+
		"      item.setState(goog.ui.Component.State.CHECKED, false);\n"+
		"    });\n"+
		"    if (isCheckingItem) {\n"+
		"      blobItem.setState(goog.ui.Component.State.CHECKED, true);\n"+
		"      this.checkedBlobItems_ = [blobItem];\n"+
		"    } else {\n"+
		"      this.checkedBlobItems_ = [];\n"+
		"    }\n"+
		"    this.dispatchEvent(camlistore.BlobItemContainer.EventType.SINGLE_NODE_CHOSEN)"+
		";\n"+
		"  }\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.unselectAll =\n"+
		"function() {\n"+
		"	goog.array.forEach(this.checkedBlobItems_, function(item) {\n"+
		"		item.setState(goog.ui.Component.State.CHECKED, false);\n"+
		"	});\n"+
		"	this.checkedBlobItems_ = [];\n"+
		"}\n"+
		"\n"+
		"/**\n"+
		" * @param {camlistore.ServerType.IndexerMetaBag} result JSON response to this req"+
		"uest.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.showRecentDone_ = function(result) {\n"+
		"  this.resetChildren_();\n"+
		"  if (!result || !result.recent) {\n"+
		"    return;\n"+
		"  }\n"+
		"  for (var i = 0, n = result.recent.length; i < n; i++) {\n"+
		"    var blobRef = result.recent[i].blobref;\n"+
		"    var item = new camlistore.BlobItem(blobRef, result.meta);\n"+
		"    this.addChild(item, true);\n"+
		"  }\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * @param {camlistore.ServerType.SearchWithAttrResponse} result JSON response to "+
		"this request.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.showWithAttrDone_ = function(result) {\n"+
		"	this.resetChildren_();\n"+
		"	if (!result) {\n"+
		"		return;\n"+
		"	}\n"+
		"	var results = result.withAttr;\n"+
		"	var meta = result.meta;\n"+
		"	if (!results || !meta) {\n"+
		"		return;\n"+
		"	}\n"+
		"	for (var i = 0, n = results.length; i < n; i++) {\n"+
		"		var blobRef = results[i].permanode;\n"+
		"		var item = new camlistore.BlobItem(blobRef, meta);\n"+
		"		this.addChild(item, true);\n"+
		"	}\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * @param {camlistore.ServerType.DescribeResponse} result JSON response to this r"+
		"equest.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.findByBlobrefDone_ =\n"+
		"function(permanode, result) {\n"+
		"	this.resetChildren_();\n"+
		"	if (!result) {\n"+
		"		return;\n"+
		"	}\n"+
		"	var meta = result.meta;\n"+
		"	if (!meta || !meta[permanode]) {\n"+
		"		return;\n"+
		"	}\n"+
		"	var item = new camlistore.BlobItem(permanode, meta);\n"+
		"	this.addChild(item, true);\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * Clears all children from this container, reseting to the default state.\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.resetChildren_ = function() {\n"+
		"  this.removeChildren(true);\n"+
		"  if (this.hasCreateItem_) {\n"+
		"    var createItem = new camlistore.CreateItem();\n"+
		"    this.addChild(createItem, true);\n"+
		"    this.eh_.listen(\n"+
		"      createItem.getElement(), goog.events.EventType.CLICK,\n"+
		"      function() {\n"+
		"        this.connection_.createPermanode(\n"+
		"            function(p) {\n"+
		"              window.location = \"./?p=\" + p;\n"+
		"            },\n"+
		"            function(failMsg) {\n"+
		"              console.log(\"Failed to create permanode: \" + failMsg);\n"+
		"            });\n"+
		"      });\n"+
		"  }\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {goog.events.Event} e The drag drop event.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.handleFileDrop_ = function(e) {\n"+
		"  this.resetDragState_();\n"+
		"\n"+
		"  var files = e.getBrowserEvent().dataTransfer.files;\n"+
		"  for (var i = 0, n = files.length; i < n; i++) {\n"+
		"    var file = files[i];\n"+
		"    // TODO(bslatkin): Add an uploading item placeholder while the upload\n"+
		"    // is in progress. Somehow pipe through the POST progress.\n"+
		"    this.connection_.uploadFile(\n"+
		"        file, goog.bind(this.handleUploadSuccess_, this, file));\n"+
		"  }\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {File} file File to upload.\n"+
		" * @param {string} blobRef BlobRef for the uploaded file.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.handleUploadSuccess_ =\n"+
		"    function(file, blobRef) {\n"+
		"  this.connection_.createPermanode(\n"+
		"      goog.bind(this.handleCreatePermanodeSuccess_, this, file, blobRef));\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {File} file File to upload.\n"+
		" * @param {string} blobRef BlobRef for the uploaded file.\n"+
		" * @param {string} permanode Permanode this blobRef is now the content of.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.handleCreatePermanodeSuccess_ =\n"+
		"    function(file, blobRef, permanode) {\n"+
		"  this.connection_.newSetAttributeClaim(\n"+
		"      permanode, 'camliContent', blobRef,\n"+
		"      goog.bind(this.handleSetAttributeSuccess_, this,\n"+
		"                file, blobRef, permanode));\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {File} file File to upload.\n"+
		" * @param {string} blobRef BlobRef for the uploaded file.\n"+
		" * @param {string} permanode Permanode this blobRef is now the content of.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.handleSetAttributeSuccess_ =\n"+
		"    function(file, blobRef, permanode) {\n"+
		"  this.connection_.describeWithThumbnails(\n"+
		"      permanode,\n"+
		"      this.thumbnailSize_,\n"+
		"      goog.bind(this.handleDescribeSuccess_, this, permanode));\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {string} permanode Node to describe.\n"+
		" * @param {camlistore.ServerType.DescribeResponse} describeResult Object of prope"+
		"rties for the node.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.handleDescribeSuccess_ =\n"+
		"  function(permanode, describeResult) {\n"+
		"  var item = new camlistore.BlobItem(permanode, describeResult.meta);\n"+
		"  this.addChildAt(item, this.hasCreateItem_ ? 1 : 0, true);\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.resetDragState_ = function() {\n"+
		"  goog.dom.classes.remove(this.getElement(),\n"+
		"                          'cam-blobitemcontainer-dropactive');\n"+
		"  this.dragActiveElement_ = null;\n"+
		"  this.dragDepth_ = 0;\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {goog.events.Event} e The drag enter event.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.handleFileDragEnter_ = function(e) {\n"+
		"  if (this.dragActiveElement_ == null) {\n"+
		"    goog.dom.classes.add(this.getElement(), 'cam-blobitemcontainer-dropactive');\n"+
		"  }\n"+
		"  this.dragDepth_ += 1;\n"+
		"  this.dragActiveElement_ = e.target;\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {goog.events.Event} e The drag leave event.\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.handleFileDragLeave_ = function(e) {\n"+
		"  this.dragDepth_ -= 1;\n"+
		"  if (this.dragActiveElement_ === this.getElement() &&\n"+
		"      e.target == this.getElement() ||\n"+
		"      this.dragDepth_ == 0) {\n"+
		"    this.resetDragState_();\n"+
		"  }\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.hide_ = function() {\n"+
		"	goog.dom.classes.remove(this.getElement(),\n"+
		"		'cam-blobitemcontainer-' + this.thumbnailSize_);\n"+
		"	goog.dom.classes.add(this.getElement(),\n"+
		"		'cam-blobitemcontainer-hidden');\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * @private\n"+
		" */\n"+
		"camlistore.BlobItemContainer.prototype.show_ = function() {\n"+
		"	goog.dom.classes.remove(this.getElement(),\n"+
		"		'cam-blobitemcontainer-hidden');\n"+
		"	goog.dom.classes.add(this.getElement(),\n"+
		"		'cam-blobitemcontainer-' + this.thumbnailSize_);\n"+
		"};\n"+
		""))
}
