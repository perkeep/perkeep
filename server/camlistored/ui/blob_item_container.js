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
   * Whether our content has changed since last layout.
   * @type {boolean}
   * @private
   */
  this.isLayoutDirty_ = false;

  /**
   * @type {goog.events.EventHandler}
   * @private
   */
  this.eh_ = new goog.events.EventHandler(this);

  /**
    * An id for a timer we use to know when the drag has ended.
    * @type {number}
    * @private
    */
  this.dragEndTimer_ = 0;

  /**
   * Whether users can drag items onto this container to upload.
   * @type {boolean}
   * @private
   */
  this.enableDragDrop_ = false;

  this.setFocusable(false);
};
goog.inherits(camlistore.BlobItemContainer, goog.ui.Container);


/**
 * @type {Array.<number>}
 */
camlistore.BlobItemContainer.THUMBNAIL_SIZES_ = [50, 75, 100, 150, 200, 250];


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
 *@param {boolean}
 */
camlistore.BlobItemContainer.prototype.setDragDropEnabled = function(val) {
  this.enableDragDrop_ = val;
}

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
};


/** @override */
camlistore.BlobItemContainer.prototype.disposeInternal = function() {
  camlistore.BlobItemContainer.superClass_.disposeInternal.call(this);
  this.eh_.dispose();
};


/** @override */
camlistore.BlobItemContainer.prototype.addChildAt =
function(child, index, opt_render) {
  goog.base(this, "addChildAt", child, index, opt_render);
  if (!this.isLayoutDirty_) {
    var raf = window.requestAnimationFrame || window.mozRequestAnimationFrame ||
      window.webkitRequestAnimationFrame || window.msRequestAnimationFrame;
    // It's OK if raf not supported, the timer loop we have going will pick up
    // the layout a little later.
    if (raf) {
      raf(goog.bind(function() {
        this.layout_(false);
      }, this, false));
    }

    this.isLayoutDirty_ = true;
  }
};


/**
 * Called when component's element is known to be in the document.
 */
camlistore.BlobItemContainer.prototype.enterDocument = function() {
  camlistore.BlobItemContainer.superClass_.enterDocument.call(this);

  this.resetChildren_();
  this.listenToBlobItemEvents_();

  if (this.enableDragDrop_) {
    this.fileDragListener_ = goog.bind(this.handleFileDrag_, this);
    this.eh_.listen(document,
                    goog.events.EventType.DRAGOVER,
                    this.fileDragListener_);
    this.eh_.listen(document,
                    goog.events.EventType.DRAGENTER,
                    this.fileDragListener_);

    this.fileDropHandler_ = new goog.events.FileDropHandler(document);
    this.registerDisposable(this.fileDropHandler_);
    this.eh_.listen(
      this.fileDropHandler_,
      goog.events.FileDropHandler.EventType.DROP,
      this.handleFileDrop_);
  }

  // We can't catch everything that could cause us to need to relayout. Instead,
  // be lazy and just poll every second.
  window.setInterval(goog.bind(function() {
    this.layout_(false);
  }, this, false), 1000);
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
 * Search and show permanodes matching the specified criteria.
 * @param {string} sigconf
 * @param {string} attr
 * @param {string} value
 * @param {boolean} fuzzy Noop because not supported yet.
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
 * Search for a permanode with the required blobref
 * @param {string} blobref
 */
camlistore.BlobItemContainer.prototype.findByBlobref_ =
function(blobref) {
	this.connection_.describeWithThumbnails(
		blobref,
		this.thumbnailSize_,
		goog.bind(this.findByBlobrefDone_, this, blobref),
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
  if (!result || !result.recent) {
    return;
  }

  for (var i = 0, n = result.recent.length; i < n; i++) {
    var blobRef = result.recent[i].blobref;
    var item = new camlistore.BlobItem(blobRef, result.meta);
    this.addChild(item, true);
  }
};

/**
 * @param {bool} Whether to force layout.
 */
camlistore.BlobItemContainer.prototype.layout_ = function(force) {
  var el = this.getElement();
  var availWidth = el.clientWidth;

  if (!force && !this.isLayoutDirty_ && availWidth == this.lastClientWidth_) {
    return;
  }

  this.isLayoutDirty_ = false;
  this.lastClientWidth_ = availWidth;

  // If you change blobItemMarginWidth, also change .cam-blobitem in
  // blob_item.css.
  var blobItemMarginWidth = 1;
  var currentWidth = 0;
  var rowStart = 0;

  var breaks = el.querySelectorAll('br');
  for (var i = 0; i < breaks.length; i++) {
    breaks[i].parentNode.removeChild(breaks[i]);
  }

  for (var i = rowStart, n = this.getChildCount(); i < n; i++) {
    var item = this.getChildAt(i);

    var nextWidth = currentWidth + item.getThumbWidth() +
        blobItemMarginWidth * 2;
    if (nextWidth < availWidth) {
      currentWidth = nextWidth;
      continue;
    }

    rowStart = this.layoutRow_(rowStart, i, currentWidth, nextWidth,
                               availWidth);
    el.insertBefore(this.dom_.createElement('br'),
                    this.getChildAt(rowStart - 1).getElement().nextSibling);
    currentWidth = 0;

    // If we didn't end up using the last element of this row, then it will be
    // the start of the next row.
    if (rowStart == i) {
      i--;
    }
  }

  // Any remaining items just get their intrinsic width.
  for (var i = rowStart; i < n; i++) {
    var item = this.getChildAt(i);
    item.resetFrameWidth();
  }
};

/**
 * @return {Number} The starting index of the next row. This can be either
 * endIndex or endIndex+1, depending on whether we were able to squeeze all the
 * items onto one row.
 */
camlistore.BlobItemContainer.prototype.layoutRow_ =
function(startIndex, endIndex, widthWithoutLastItem, widthWithLastItem,
         availWidth) {
  // We are trying to create rows that completely fill their container. There
  // are two ways we can do that, and we need to decide which is better:
  //
  // - For images, we can set the frame narrower than the image actually
  //   requires, clipping some of the horizontal width.
  // - For non-images, clipping doesn't look good because they use icons and
  //   it's obvious when they've been clipped. For these, we can do the opposite
  //   and add additional horizontal padding.
  //
  // To determine which strategy to use, we calculate how much each item would
  // change, and use the strategy with the lowest per-item impact.
  var clipLayoutData = {
    items: [],
    endIndex: endIndex,
    width: widthWithLastItem
  };
  var stretchLayoutData = {
    items: [],
    endIndex: endIndex - 1,
    width: widthWithoutLastItem
  };

  for (var i = startIndex; i <= endIndex; i++) {
    var item = this.getChildAt(i);
    if (item.isImage()) {
      clipLayoutData.items.push(i);
    } else if (i < endIndex) {
      stretchLayoutData.items.push(i);
    }
  }

  function perItemChange(layoutData) {
    // If we don't have any items with this layout type, then no amount of
    // per-item change will help.
    if (!layoutData.items.length) {
      return Number.POSITIVE_INFINITY;
    }

    return Math.abs((availWidth - layoutData.width) / layoutData.items.length);
  }

  function setFrames(layoutData) {
    var overflow = layoutData.width - availWidth;
    for (var i = startIndex; i <= layoutData.endIndex; i++) {
      var item = this.getChildAt(i);
      if (i == layoutData.items[0]) {
        var portion = Math.ceil(overflow / layoutData.items.length);
        item.setFrameWidth(item.getThumbWidth() - portion);
        layoutData.items.shift();
        overflow -= portion;
      } else {
        item.resetFrameWidth();
      }
    }
  }

  if (perItemChange(clipLayoutData) <= perItemChange(stretchLayoutData)) {
    setFrames.call(this, clipLayoutData);
    return endIndex + 1;
  } else {
    setFrames.call(this, stretchLayoutData);
    return endIndex;
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
 * @param {camlistore.ServerType.DescribeResponse} result JSON response to this request.
 * @private
 */
camlistore.BlobItemContainer.prototype.findByBlobrefDone_ =
function(permanode, result) {
	this.resetChildren_();
	if (!result) {
		return;
	}
	var meta = result.meta;
	if (!meta || !meta[permanode]) {
		return;
	}
	var item = new camlistore.BlobItem(permanode, meta);
	this.addChild(item, true);
};

/**
 * Clears all children from this container, reseting to the default state.
 */
camlistore.BlobItemContainer.prototype.resetChildren_ = function() {
  this.removeChildren(true);
};


/**
 * @param {goog.events.Event} e The drag drop event.
 * @private
 */
camlistore.BlobItemContainer.prototype.handleFileDrop_ = function(e) {
  var recipient = this.dragActiveElement_;
  if (!recipient) {
    console.log("No valid target to drag and drop on.");
    return;
  }

  goog.dom.classes.remove(recipient.getElement(), 'cam-dropactive');
  this.dragActiveElement_ = null;

  var files = e.getBrowserEvent().dataTransfer.files;
  for (var i = 0, n = files.length; i < n; i++) {
    var file = files[i];
    // TODO(bslatkin): Add an uploading item placeholder while the upload
    // is in progress. Somehow pipe through the POST progress.
    this.connection_.uploadFile(
      file, goog.bind(this.handleUploadSuccess_, this, file, recipient.blobRef_));
  }
};


/**
 * @param {File} file File to upload.
 * @param {string} blobRef BlobRef for the uploaded file.
 * @private
 */
camlistore.BlobItemContainer.prototype.handleUploadSuccess_ =
    function(file, recipient, blobRef) {
  this.connection_.createPermanode(
      goog.bind(this.handleCreatePermanodeSuccess_, this, file, recipient, blobRef));
};


/**
 * @param {File} file File to upload.
 * @param {string} blobRef BlobRef for the uploaded file.
 * @param {string} permanode Permanode this blobRef is now the content of.
 * @private
 */
camlistore.BlobItemContainer.prototype.handleCreatePermanodeSuccess_ =
    function(file, recipient, blobRef, permanode) {
  this.connection_.newSetAttributeClaim(
      permanode, 'camliContent', blobRef,
      goog.bind(this.handleSetAttributeSuccess_, this,
                file, recipient, blobRef, permanode));
};


/**
 * @param {File} file File to upload.
 * @param {string} blobRef BlobRef for the uploaded file.
 * @param {string} permanode Permanode this blobRef is now the content of.
 * @private
 */
camlistore.BlobItemContainer.prototype.handleSetAttributeSuccess_ =
function(file, recipient, blobRef, permanode) {
	this.connection_.describeWithThumbnails(
		permanode,
		this.thumbnailSize_,
		goog.bind(this.handleDescribeSuccess_, this, recipient, permanode));
};


/**
 * @param {string} permanode Node to describe.
 * @param {camlistore.ServerType.DescribeResponse} describeResult Object of properties for the node.
 * @private
 */
camlistore.BlobItemContainer.prototype.handleDescribeSuccess_ =
  function(recipient, permanode, describeResult) {
	var item = new camlistore.BlobItem(permanode, describeResult.meta);
	this.addChildAt(item, 0, true);
	if (!recipient) {
		return;
	}
	this.connection_.newAddAttributeClaim(
		recipient, 'camliMember', permanode);
};

/**
 * @param {goog.events.Event} e The drag enter event.
 * @private
 */
camlistore.BlobItemContainer.prototype.handleFileDrag_ = function(e) {
  if (this.dragEndTimer_) {
    this.dragEndTimer_ = window.clearTimeout(this.dragEndTimer_);
  }
  this.dragEndTimer_ = window.setTimeout(this.fileDragListener_, 2000);

  var activeElement = e ? this.getOwnerControl(e.target) : e;
  if (activeElement) {
    if (!activeElement.isCollection()) {
      activeElement = this;
    }
  } else if (e) {
    // TODO(aa): Seems like there should be an easier way to do this.
    var viewportPoint = goog.style.getClientPosition(this.getElement());
    var viewportRect = goog.style.getBounds(this.getElement());
    viewportRect.x = viewportPoint.x;
    viewportRect.y = viewportPoint.y;
    if (viewportRect.contains(goog.style.getClientPosition(e))) {
      activeElement = this;
    }
  }

  if (activeElement == this.dragActiveElement_) {
    return;
  }

  if (this.dragActiveElement_) {
    goog.dom.classes.remove(this.dragActiveElement_.getElement(),
                            'cam-dropactive');
  }

  this.dragActiveElement_ = activeElement;

  if (this.dragActiveElement_) {
    goog.dom.classes.add(this.dragActiveElement_.getElement(),
                         'cam-dropactive');
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
  this.layout_(true);
};
