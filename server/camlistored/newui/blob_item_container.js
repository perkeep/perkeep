/**
 * @fileoverview Contains a set of BlobItems. Knows how to fetch items from
 * the server side. Is preconfigured with common queries like "recent" blobs.
 *
 */
goog.provide('camlistore.BlobItemContainer');

goog.require('goog.dom');
goog.require('goog.dom.classes');
goog.require('goog.events.Event');
goog.require('goog.events.EventHandler');
goog.require('goog.events.EventType');
goog.require('goog.events.FileDropHandler');
goog.require('goog.ui.Container');
goog.require('camlistore.BlobItem');
goog.require('camlistore.CreateItem');
goog.require('camlistore.ServerConnection');


/**
 * @param {camlistore.ServerConnection} connection Connection to the server
 *   for fetching blobrefs and other queries.
 * @param {goog.dom.DomHelper=} opt_domHelper DOM helper to use.
 *
 * @extends {goog.ui.Container}
 * @constructor
 */
camlistore.BlobItemContainer = function(connection, opt_domHelper) {
  goog.base(this, opt_domHelper);

  /**
   * @type {Array.<camlistore.BlobItem>}
   * @private
   */
  this.checkedBlobItems_ = [];

  /**
   * @type {camlistore.ServerConnection}
   * @private
   */
  this.connection_ = connection;

  /**
   * BlobRef of the permanode defined as the current collection/set.
   * Selected blobitems will be added as members of that collection
   * upon relevant actions (e.g click on the 'Add to Set' toolbar button).
   * @type {string}
   * @private
   */
  this.currentCollec_ = "";

  /**
   * @type {goog.events.EventHandler}
   * @private
   */
  this.eh_ = new goog.events.EventHandler(this);
};
goog.inherits(camlistore.BlobItemContainer, goog.ui.Container);


/**
 * @type {Array.<number>}
 */
camlistore.BlobItemContainer.THUMBNAIL_SIZES_ = [25, 50, 75, 100, 150, 200];


/**
 * @type {goog.events.FileDropHandler}
 * @private
 */
camlistore.BlobItemContainer.prototype.fileDropHandler_ = null;


/**
 * @type {Element}
 * @private
 */
camlistore.BlobItemContainer.prototype.dragActiveElement_ = null;


/**
 * @type {number}
 * @private
 */
camlistore.BlobItemContainer.prototype.dragDepth_ = 0;


/**
 * Constants for events fired by BlobItemContainer
 * @enum {string}
 */
camlistore.BlobItemContainer.EventType = {
  BLOB_ITEMS_CHOSEN: 'Camlistore_BlobItemContainer_BlobItems_Chosen',
  SINGLE_NODE_CHOSEN: 'Camlistore_BlobItemContainer_SingleNode_Chosen'
};


/**
 * @type {number}
 * @private
 */
camlistore.BlobItemContainer.prototype.thumbnailSize_ = 100;


/**
 * @type {boolean}
 * @private
 */
camlistore.BlobItemContainer.prototype.hasCreateItem_ = false;


/**
 * @return {boolean}
 */
camlistore.BlobItemContainer.prototype.smaller = function() {
  var index = camlistore.BlobItemContainer.THUMBNAIL_SIZES_.indexOf(
      this.thumbnailSize_);
  if (index == 0) {
    return false;
  }
  var el = this.getElement();
  goog.dom.classes.remove(el, 'cam-blobitemcontainer-' + this.thumbnailSize_);
  this.thumbnailSize_ = camlistore.BlobItemContainer.THUMBNAIL_SIZES_[index-1];
  goog.dom.classes.add(el, 'cam-blobitemcontainer-' + this.thumbnailSize_);
  return true;
};


/**
 * @return {boolean}
 */
camlistore.BlobItemContainer.prototype.bigger = function() {
  var index = camlistore.BlobItemContainer.THUMBNAIL_SIZES_.indexOf(
      this.thumbnailSize_);
  if (index == camlistore.BlobItemContainer.THUMBNAIL_SIZES_.length - 1) {
    return false;
  }
  var el = this.getElement();
  goog.dom.classes.remove(el, 'cam-blobitemcontainer-' + this.thumbnailSize_);
  this.thumbnailSize_ = camlistore.BlobItemContainer.THUMBNAIL_SIZES_[index+1];
  goog.dom.classes.add(el, 'cam-blobitemcontainer-' + this.thumbnailSize_);
  return true;
};


/**
 * @param {boolean} v
 */
camlistore.BlobItemContainer.prototype.setHasCreateItem = function(v) {
  this.hasCreateItem_ = v;
};


/**
 * Creates an initial DOM representation for the component.
 */
camlistore.BlobItemContainer.prototype.createDom = function() {
  this.decorateInternal(this.dom_.createElement('div'));
};


/**
 * Decorates an existing HTML DIV element.
 * @param {Element} element The DIV element to decorate.
 */
camlistore.BlobItemContainer.prototype.decorateInternal = function(element) {
  camlistore.BlobItemContainer.superClass_.decorateInternal.call(this, element);

  var el = this.getElement();
  goog.dom.classes.add(el, 'cam-blobitemcontainer');
  goog.dom.classes.add(el, 'cam-blobitemcontainer-' + this.thumbnailSize_);

  var dropMessageEl = this.dom_.createDom(
      'div', 'cam-blobitemcontainer-drag-message',
      'Drag & drop item to upload.');
  var dropIndicatorEl = this.dom_.createDom(
      'div', 'cam-blobitemcontainer-drag-indicator');
  this.dom_.appendChild(dropIndicatorEl, dropMessageEl);
  this.dom_.appendChild(el, dropIndicatorEl);
};


/** @override */
camlistore.BlobItemContainer.prototype.disposeInternal = function() {
  camlistore.BlobItemContainer.superClass_.disposeInternal.call(this);
  this.eh_.dispose();
};


/**
 * Called when component's element is known to be in the document.
 */
camlistore.BlobItemContainer.prototype.enterDocument = function() {
  camlistore.BlobItemContainer.superClass_.enterDocument.call(this);

  this.resetChildren_();
  this.listenToBlobItemEvents_();

  this.fileDropHandler_ = new goog.events.FileDropHandler(
      this.getElement());
  this.registerDisposable(this.fileDropHandler_);
  this.eh_.listen(
      this.fileDropHandler_,
      goog.events.FileDropHandler.EventType.DROP,
      this.handleFileDrop_);
  this.eh_.listen(
      this.getElement(),
      goog.events.EventType.DRAGENTER,
      this.handleFileDragEnter_);
  this.eh_.listen(
      this.getElement(),
      goog.events.EventType.DRAGLEAVE,
      this.handleFileDragLeave_);
};


/**
 * Called when component's element is known to have been removed from the
 * document.
 */
camlistore.BlobItemContainer.prototype.exitDocument = function() {
  camlistore.BlobItemContainer.superClass_.exitDocument.call(this);
  this.eh_.removeAll();
};


/**
 * Show recent blobs.
 */
camlistore.BlobItemContainer.prototype.showRecent = function() {
  this.connection_.getRecentlyUpdatedPermanodes(
      goog.bind(this.showRecentDone_, this),
      this.thumbnailSize_);
};


/**
 * Show roots
 */
camlistore.BlobItemContainer.prototype.showRoots =
function(sigconf) {
	this.connection_.permanodesWithAttr(sigconf.publicKeyBlobRef,
		"camliRoot", "", false, 0, this.thumbnailSize_,
		goog.bind(this.showWithAttrDone_, this),
		function(msg) {
			alert(msg);
		}
	);
};

/**
 * Show tagged
 * @param {string} sigconf
 * @param {string} attr
 * @param {string} value
 * @param {boolean} fuzzy
 * @param {number} max max number of items in response.
 */
camlistore.BlobItemContainer.prototype.showWithAttr =
function(sigconf, attr, value, fuzzy, max) {
	this.connection_.permanodesWithAttr(sigconf.publicKeyBlobRef,
		attr, value, fuzzy, max, this.thumbnailSize_,
		goog.bind(this.showWithAttrDone_, this),
		function(msg) {
			alert(msg);
		}
	);
};

/**
 * @return {Array.<camlistore.BlobItem>}
 */
camlistore.BlobItemContainer.prototype.getCheckedBlobItems = function() {
  return this.checkedBlobItems_;
};


/**
 * Subscribes to events dispatched by blob items.
 * @private
 */
camlistore.BlobItemContainer.prototype.listenToBlobItemEvents_ = function() {
  var doc = goog.dom.getOwnerDocument(this.element_);
  this.eh_.
      listen(this, goog.ui.Component.EventType.CHECK,
             this.handleBlobItemChecked_).
      listen(this, goog.ui.Component.EventType.UNCHECK,
             this.handleBlobItemChecked_).
      listen(doc,
             goog.events.EventType.KEYDOWN,
             this.handleKeyDownEvent_).
      listen(doc,
             goog.events.EventType.KEYUP,
             this.handleKeyUpEvent_);
};


/**
 * @type {boolean}
 * @private
 */
camlistore.BlobItemContainer.prototype.isShiftKeyDown_ = false;


/**
 * @type {boolean}
 * @private
 */
camlistore.BlobItemContainer.prototype.isCtrlKeyDown_ = false;


/**
 * Sets state for whether or not the shift or ctrl key is down.
 * @param {goog.events.KeyEvent} e A key event.
 */
camlistore.BlobItemContainer.prototype.handleKeyDownEvent_ = function(e) {
  if (e.keyCode == goog.events.KeyCodes.SHIFT) {
    this.isShiftKeyDown_ = true;
    this.isCtrlKeyDown_ = false;
    return;
  }
  if (e.keyCode == goog.events.KeyCodes.CTRL) {
    this.isCtrlKeyDown_ = true;
    this.isShiftKeyDown_ = false;
    return;
  }
};


/**
 * Sets state for whether or not the shift or ctrl key is up.
 * @param {goog.events.KeyEvent} e A key event.
 */
camlistore.BlobItemContainer.prototype.handleKeyUpEvent_ = function(e) {
  this.isShiftKeyDown_ = false;
  this.isCtrlKeyDown_ = false;
};


/**
 * @param {goog.events.Event} e An event.
 * @private
 */
camlistore.BlobItemContainer.prototype.handleBlobItemChecked_ = function(e) {
  // Because the CHECK/UNCHECK event dispatches before isChecked is set.
  // We stop the default behaviour because want to control manually here whether
  // the source blobitem gets checked or not. See http://camlistore.org/issue/134
  e.preventDefault();
  var blobItem = e.target;
  var isCheckingItem = !blobItem.isChecked();
  var isShiftMultiSelect = this.isShiftKeyDown_;
  var isCtrlMultiSelect = this.isCtrlKeyDown_;

  if (isShiftMultiSelect || isCtrlMultiSelect) {
    var lastChildSelected =
        this.checkedBlobItems_[this.checkedBlobItems_.length - 1];
    var firstChildSelected =
        this.checkedBlobItems_[0];
    var lastChosenIndex = this.indexOfChild(lastChildSelected);
    var firstChosenIndex = this.indexOfChild(firstChildSelected);
    var thisIndex = this.indexOfChild(blobItem);
  }

  if (isShiftMultiSelect) {
    // deselect all items after the chosen one
    for (var i = lastChosenIndex; i > thisIndex; i--) {
      var item = this.getChildAt(i);
      item.setState(goog.ui.Component.State.CHECKED, false);
      if (goog.array.contains(this.checkedBlobItems_, item)) {
        goog.array.remove(this.checkedBlobItems_, item);
      }
    }
    // make sure all the others are selected.
    for (var i = firstChosenIndex; i <= thisIndex; i++) {
      var item = this.getChildAt(i);
      item.setState(goog.ui.Component.State.CHECKED, true);
      if (!goog.array.contains(this.checkedBlobItems_, item)) {
        this.checkedBlobItems_.push(item);
      }
    }
    this.dispatchEvent(camlistore.BlobItemContainer.EventType.BLOB_ITEMS_CHOSEN);
  } else if (isCtrlMultiSelect) {
    if (isCheckingItem) {
      blobItem.setState(goog.ui.Component.State.CHECKED, true);
      if (!goog.array.contains(this.checkedBlobItems_, blobItem)) {
        var pos = -1;
        for (var i = 0; i <= this.checkedBlobItems_.length; i++) {
          var idx = this.indexOfChild(this.checkedBlobItems_[i]);
          if (idx > thisIndex) {
            pos = i;
            break;
          }
        }
        if (pos != -1) {
          goog.array.insertAt(this.checkedBlobItems_, blobItem, pos)
        } else {
          this.checkedBlobItems_.push(blobItem);
        }
      }
    } else {
      blobItem.setState(goog.ui.Component.State.CHECKED, false);
      if (goog.array.contains(this.checkedBlobItems_, blobItem)) {
        var done = goog.array.remove(this.checkedBlobItems_, blobItem);
        if (!done) {
          alert("Failed to remove item from selection");
        }
      }
    }
    this.dispatchEvent(camlistore.BlobItemContainer.EventType.BLOB_ITEMS_CHOSEN);
  } else {
    // unselect all chosen items.
    goog.array.forEach(this.checkedBlobItems_, function(item) {
      item.setState(goog.ui.Component.State.CHECKED, false);
    });
    if (isCheckingItem) {
      blobItem.setState(goog.ui.Component.State.CHECKED, true);
      this.checkedBlobItems_ = [blobItem];
    } else {
      this.checkedBlobItems_ = [];
    }
    this.dispatchEvent(camlistore.BlobItemContainer.EventType.SINGLE_NODE_CHOSEN);
  }
};

/**
 */
camlistore.BlobItemContainer.prototype.unselectAll =
function() {
	goog.array.forEach(this.checkedBlobItems_, function(item) {
		item.setState(goog.ui.Component.State.CHECKED, false);
	});
	this.checkedBlobItems_ = [];
}

/**
 * @param {camlistore.ServerType.IndexerMetaBag} result JSON response to this request.
 * @private
 */
camlistore.BlobItemContainer.prototype.showRecentDone_ = function(result) {
  this.resetChildren_();
  for (var i = 0, n = result.recent.length; i < n; i++) {
    var blobRef = result.recent[i].blobref;
    var item = new camlistore.BlobItem(blobRef, result.meta);
    this.addChild(item, true);
  }
};

/**
 * @param {camlistore.ServerType.SearchWithAttrResponse} result JSON response to this request.
 * @private
 */
camlistore.BlobItemContainer.prototype.showWithAttrDone_ = function(result) {
	this.resetChildren_();
	if (!result) {
		return;
	}
	var results = result.withAttr;
	var meta = result.meta;
	if (!results || !meta) {
		return;
	}
	for (var i = 0, n = results.length; i < n; i++) {
		var blobRef = results[i].permanode;
		var item = new camlistore.BlobItem(blobRef, meta);
		this.addChild(item, true);
	}
};

/**
 * Clears all children from this container, reseting to the default state.
 */
camlistore.BlobItemContainer.prototype.resetChildren_ = function() {
  this.removeChildren(true);
  if (this.hasCreateItem_) {
    var createItem = new camlistore.CreateItem();
    this.addChild(createItem, true);
    this.eh_.listen(
      createItem.getElement(), goog.events.EventType.CLICK,
      function() {
        this.connection_.createPermanode(
            function(p) {
              window.location = "../?p=" + p;
            },
            function(failMsg) {
              console.log("Failed to create permanode: " + failMsg);
            });
      });
  }
};


/**
 * @param {goog.events.Event} e The drag drop event.
 * @private
 */
camlistore.BlobItemContainer.prototype.handleFileDrop_ = function(e) {
  this.resetDragState_();

  var files = e.getBrowserEvent().dataTransfer.files;
  for (var i = 0, n = files.length; i < n; i++) {
    var file = files[i];
    // TODO(bslatkin): Add an uploading item placeholder while the upload
    // is in progress. Somehow pipe through the POST progress.
    this.connection_.uploadFile(
        file, goog.bind(this.handleUploadSuccess_, this, file));
  }
};


/**
 * @param {File} file File to upload.
 * @param {string} blobRef BlobRef for the uploaded file.
 * @private
 */
camlistore.BlobItemContainer.prototype.handleUploadSuccess_ =
    function(file, blobRef) {
  this.connection_.createPermanode(
      goog.bind(this.handleCreatePermanodeSuccess_, this, file, blobRef));
};


/**
 * @param {File} file File to upload.
 * @param {string} blobRef BlobRef for the uploaded file.
 * @param {string} permanode Permanode this blobRef is now the content of.
 * @private
 */
camlistore.BlobItemContainer.prototype.handleCreatePermanodeSuccess_ =
    function(file, blobRef, permanode) {
  this.connection_.newSetAttributeClaim(
      permanode, 'camliContent', blobRef,
      goog.bind(this.handleSetAttributeSuccess_, this,
                file, blobRef, permanode));
};


/**
 * @param {File} file File to upload.
 * @param {string} blobRef BlobRef for the uploaded file.
 * @param {string} permanode Permanode this blobRef is now the content of.
 * @private
 */
camlistore.BlobItemContainer.prototype.handleSetAttributeSuccess_ =
    function(file, blobRef, permanode) {
  this.connection_.describeWithThumbnails(
      permanode,
      this.thumbnailSize_,
      goog.bind(this.handleDescribeSuccess_, this, permanode));
};


/**
 * @param {string} permanode Node to describe.
 * @param {Object} describeResult Object of properties for the node.
 * @private
 */
camlistore.BlobItemContainer.prototype.handleDescribeSuccess_ =
  function(permanode, describeResult) {
  var item = new camlistore.BlobItem(permanode, describeResult.meta);
  this.addChildAt(item, this.hasCreateItem_ ? 1 : 0, true);
};


/**
 * @private
 */
camlistore.BlobItemContainer.prototype.resetDragState_ = function() {
  goog.dom.classes.remove(this.getElement(),
                          'cam-blobitemcontainer-dropactive');
  this.dragActiveElement_ = null;
  this.dragDepth_ = 0;
};


/**
 * @param {goog.events.Event} e The drag enter event.
 * @private
 */
camlistore.BlobItemContainer.prototype.handleFileDragEnter_ = function(e) {
  if (this.dragActiveElement_ == null) {
    goog.dom.classes.add(this.getElement(), 'cam-blobitemcontainer-dropactive');
  }
  this.dragDepth_ += 1;
  this.dragActiveElement_ = e.target;
};


/**
 * @param {goog.events.Event} e The drag leave event.
 * @private
 */
camlistore.BlobItemContainer.prototype.handleFileDragLeave_ = function(e) {
  this.dragDepth_ -= 1;
  if (this.dragActiveElement_ === this.getElement() &&
      e.target == this.getElement() ||
      this.dragDepth_ == 0) {
    this.resetDragState_();
  }
};

/**
 * @private
 */
camlistore.BlobItemContainer.prototype.hide_ = function() {
	goog.dom.classes.remove(this.getElement(),
		'cam-blobitemcontainer-' + this.thumbnailSize_);
	goog.dom.classes.add(this.getElement(),
		'cam-blobitemcontainer-hidden');
};

/**
 * @private
 */
camlistore.BlobItemContainer.prototype.show_ = function() {
	goog.dom.classes.remove(this.getElement(),
		'cam-blobitemcontainer-hidden');
	goog.dom.classes.add(this.getElement(),
		'cam-blobitemcontainer-' + this.thumbnailSize_);
};
