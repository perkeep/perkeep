/*
Copyright 2013 Google Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

goog.provide('cam.BlobItemContainer');

goog.require('goog.dom');
goog.require('goog.dom.classes');
goog.require('goog.events.Event');
goog.require('goog.events.EventHandler');
goog.require('goog.events.EventType');
goog.require('goog.events.FileDropHandler');
goog.require('goog.ui.Container');

goog.require('cam.BlobItem');
goog.require('cam.SearchSession');
goog.require('cam.ServerConnection');

// An infinite scrolling list of BlobItem. The heights of rows and clip of individual items is adjusted to get a fully justified appearance.
cam.BlobItemContainer = function(connection, opt_domHelper) {
	goog.base(this, opt_domHelper);

	this.checkedBlobItems_ = [];

	this.connection_ = connection;

	this.searchSession_ = null;

	this.eh_ = new goog.events.EventHandler(this);

	// BlobRef of the permanode defined as the current collection/set. Selected blobitems will be added as members of that collection upon relevant actions (e.g click on the 'Add to Set' toolbar button).
	this.currentCollec_ = "";

	// Whether our content has changed since last layout.
	this.isLayoutDirty_ = false;

	// An id for a timer we use to know when the drag has ended.
	this.dragEndTimer_ = 0;

	// Whether the blobItems within can be selected.
	this.isSelectionEnabled = false;

	// Whether users can drag files onto the container to upload.
	this.isFileDragEnabled = false;

	// A lookup of blobRef->cam.BlobItem. This allows us to quickly find and reuse existing controls when we're updating the UI in response to a server push.
	this.itemCache_ = {};

	this.setFocusable(false);
};
goog.inherits(cam.BlobItemContainer, goog.ui.Container);

// Margin between items in the layout.
cam.BlobItemContainer.BLOB_ITEM_MARGIN = 7;

// If the last row uses at least this much of the available width before adjustments, we'll call it "close enough" and adjust things so that it fills the entire row. Less than this, and we'll leave the last row unaligned.
cam.BlobItemContainer.LAST_ROW_CLOSE_ENOUGH_TO_FULL = 0.85;

cam.BlobItemContainer.THUMBNAIL_SIZES_ = [75, 100, 150, 200, 250];

// Distance from the bottom of the page at which we will trigger loading more data.
cam.BlobItemContainer.INFINITE_SCROLL_THRESHOLD_PX_ = 100;

cam.BlobItemContainer.NUM_ITEMS_PER_PAGE = 50;

cam.BlobItemContainer.prototype.fileDropHandler_ = null;

cam.BlobItemContainer.prototype.dragActiveElement_ = null;

// Constants for events fired by BlobItemContainer
cam.BlobItemContainer.EventType = {
	SELECTION_CHANGED: 'Camlistore_BlobItemContainer_SelectionChanged',
};

cam.BlobItemContainer.prototype.thumbnailSize_ = 200;

cam.BlobItemContainer.prototype.smaller = function() {
	var index = cam.BlobItemContainer.THUMBNAIL_SIZES_.indexOf(this.thumbnailSize_);
	if (index == 0) {
		return false;
	}
	var el = this.getElement();
	goog.dom.classes.remove(el, 'cam-blobitemcontainer-' + this.thumbnailSize_);
	this.thumbnailSize_ = cam.BlobItemContainer.THUMBNAIL_SIZES_[index-1];
	goog.dom.classes.add(el, 'cam-blobitemcontainer-' + this.thumbnailSize_);
	return true;
};

cam.BlobItemContainer.prototype.bigger = function() {
	var index = cam.BlobItemContainer.THUMBNAIL_SIZES_.indexOf(
			this.thumbnailSize_);
	if (index == cam.BlobItemContainer.THUMBNAIL_SIZES_.length - 1) {
		return false;
	}
	var el = this.getElement();
	goog.dom.classes.remove(el, 'cam-blobitemcontainer-' + this.thumbnailSize_);
	this.thumbnailSize_ = cam.BlobItemContainer.THUMBNAIL_SIZES_[index+1];
	goog.dom.classes.add(el, 'cam-blobitemcontainer-' + this.thumbnailSize_);
	return true;
};

cam.BlobItemContainer.prototype.createDom = function() {
	this.decorateInternal(this.dom_.createElement('div'));
};

cam.BlobItemContainer.prototype.decorateInternal = function(element) {
	cam.BlobItemContainer.superClass_.decorateInternal.call(this, element);
	this.layout_();

	var el = this.getElement();
	goog.dom.classes.add(el, 'cam-blobitemcontainer');
	goog.dom.classes.add(el, 'cam-blobitemcontainer-' + this.thumbnailSize_);
};

cam.BlobItemContainer.prototype.disposeInternal = function() {
	cam.BlobItemContainer.superClass_.disposeInternal.call(this);
	this.eh_.dispose();
};

cam.BlobItemContainer.prototype.addChildAt = function(child, index, opt_render) {
	goog.base(this, "addChildAt", child, index, opt_render);
	child.setEnabled(this.isSelectionEnabled);
	if (!this.isLayoutDirty_) {
		var raf = window.requestAnimationFrame || window.mozRequestAnimationFrame || window.webkitRequestAnimationFrame || window.msRequestAnimationFrame;
		// It's OK if raf not supported, the timer loop we have going will pick up the layout a little later.
		if (raf) {
			raf(goog.bind(this.layout_, this, false));
		}

		this.isLayoutDirty_ = true;
	}
};

cam.BlobItemContainer.prototype.removeChildAt = function(index, opt_render) {
	goog.base(this, "removeChildAt", index, opt_render);
	this.isLayoutDirty_ = true;
};

cam.BlobItemContainer.prototype.enterDocument = function() {
	cam.BlobItemContainer.superClass_.enterDocument.call(this);

	this.resetChildren_();
	this.listenToBlobItemEvents_();

	if (this.isFileDragEnabled) {
		this.fileDragListener_ = goog.bind(this.handleFileDrag_, this);
		this.eh_.listen(document, goog.events.EventType.DRAGOVER, this.fileDragListener_);
		this.eh_.listen(document, goog.events.EventType.DRAGENTER, this.fileDragListener_);

		this.fileDropHandler_ = new goog.events.FileDropHandler(document);
		this.registerDisposable(this.fileDropHandler_);
		this.eh_.listen(this.fileDropHandler_, goog.events.FileDropHandler.EventType.DROP, this.handleFileDrop_);
	}

	this.eh_.listen(document, goog.events.EventType.SCROLL, this.handleScroll_);

	// We can't catch everything that could cause us to need to relayout. Instead, be lazy and just poll every second.
	window.setInterval(goog.bind(this.layout_, this, false), 1000);
};

cam.BlobItemContainer.prototype.exitDocument = function() {
	cam.BlobItemContainer.superClass_.exitDocument.call(this);
	this.eh_.removeAll();
};

cam.BlobItemContainer.prototype.showSearchSession = function(session) {
	var changeType = cam.SearchSession.SEARCH_SESSION_CHANGE_TYPE.APPEND;

	if (this.searchSession_ != session) {
		if (this.searchSession_) {
			this.eh_.unlisten(this.searchSession_, cam.SearchSession.SEARCH_SESSION_CHANGED, this.searchDone_);
		}
		this.resetChildren_();
		this.itemCache_ = {};
		this.layout_();
		this.searchSession_ = session;
		this.eh_.listen(session, cam.SearchSession.SEARCH_SESSION_CHANGED, this.searchDone_);
		changeType = cam.SearchSession.SEARCH_SESSION_CHANGE_TYPE.NEW;
	}

	this.searchDone_({changeType:changeType});
};

cam.BlobItemContainer.prototype.getSearchSession = function() {
	return this.searchSession_;
};

cam.BlobItemContainer.prototype.searchDone_ = function(e) {
	if (e.changeType == cam.SearchSession.SEARCH_SESSION_CHANGE_TYPE.NEW) {
		this.resetChildren_();
		this.itemCache_ = {};
	}

	this.populateChildren_(this.searchSession_.getCurrentResults(), e.changeType == cam.SearchSession.SEARCH_SESSION_CHANGE_TYPE.APPEND);

	if (this.searchSession_.isComplete()) {
		return;
	}

	// If we haven't filled the window with results, add some more.
	this.layout_();
	var docHeight = goog.dom.getDocumentHeight();
	var viewportHeight = goog.dom.getViewportSize().height;
	if (docHeight < (viewportHeight * 1.5)) {
		this.searchSession_.loadMoreResults();
	}
};

cam.BlobItemContainer.prototype.findByBlobref_ = function(blobref) {
	this.connection_.describeWithThumbnails(
		blobref, this.thumbnailSize_,
		goog.bind(this.findByBlobrefDone_, this, blobref),
		function(msg) { alert(msg); });
};

cam.BlobItemContainer.prototype.getCheckedBlobItems = function() {
	return this.checkedBlobItems_;
};

cam.BlobItemContainer.prototype.listenToBlobItemEvents_ = function() {
	var doc = goog.dom.getOwnerDocument(this.element_);
	this.eh_.listen(this, goog.ui.Component.EventType.CHECK, this.handleBlobItemChecked_);
	this.eh_.listen(this, goog.ui.Component.EventType.UNCHECK, this.handleBlobItemChecked_);
	this.eh_.listen(doc, goog.events.EventType.KEYDOWN, this.handleKeyDownEvent_);
	this.eh_.listen(doc, goog.events.EventType.KEYUP, this.handleKeyUpEvent_);
};

cam.BlobItemContainer.prototype.isShiftKeyDown_ = false;

cam.BlobItemContainer.prototype.isCtrlKeyDown_ = false;

// Sets state for whether or not the shift or ctrl key is down.
cam.BlobItemContainer.prototype.handleKeyDownEvent_ = function(e) {
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

// Sets state for whether or not the shift or ctrl key is up.
cam.BlobItemContainer.prototype.handleKeyUpEvent_ = function(e) {
	this.isShiftKeyDown_ = false;
	this.isCtrlKeyDown_ = false;
};

cam.BlobItemContainer.prototype.handleBlobItemChecked_ = function(e) {
	// Because the CHECK/UNCHECK event dispatches before isChecked is set.
	// We stop the default behaviour because want to control manually here whether
	// the source blobitem gets checked or not. See http://cam.org/issue/134
	e.preventDefault();
	var blobItem = e.target;
	var isCheckingItem = !blobItem.isChecked();
	var isShiftMultiSelect = this.isShiftKeyDown_;
	var isCtrlMultiSelect = this.isCtrlKeyDown_;

	if (isShiftMultiSelect || isCtrlMultiSelect) {
		var lastChildSelected = this.checkedBlobItems_[this.checkedBlobItems_.length - 1];
		var firstChildSelected = this.checkedBlobItems_[0];
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
	} else {
		blobItem.setState(goog.ui.Component.State.CHECKED, isCheckingItem);
		if (isCheckingItem) {
			this.checkedBlobItems_.push(blobItem);
		} else {
			goog.array.remove(this.checkedBlobItems_, blobItem);
		}
	}
	this.dispatchEvent(cam.BlobItemContainer.EventType.SELECTION_CHANGED);
};

cam.BlobItemContainer.prototype.unselectAll = function() {
	goog.array.forEach(this.checkedBlobItems_, function(item) {
		item.setState(goog.ui.Component.State.CHECKED, false);
	});
	this.checkedBlobItems_ = [];
	this.dispatchEvent(cam.BlobItemContainer.EventType.SELECTION_CHANGED);
};

cam.BlobItemContainer.prototype.populateChildren_ = function(result, append) {
	var i = append ? this.getChildCount() : 0;
	for (var blob; blob = result.blobs[i]; i++) {
		var blobRef = blob.blob;
		var item = this.itemCache_[blobRef];
		var render = true;

		// If there's already an item for this blob, reuse it so that we don't lose any of the UI state (like whether it is selected).
		if (item) {
			item.update(blobRef, result.description.meta);
			item.updateDom();
			render = false;
		} else {
			item = new cam.BlobItem(blobRef, result.description.meta);
			this.itemCache_[blobRef] = item;
		}

		if (append) {
			this.addChild(item, render);
		} else {
			this.addChildAt(item, i, render);
		}
	}

	// Remove any children we don't need anymore.
	if (!append) {
		var numBlobs = result.blobs.length;
		while (this.getChildCount() > numBlobs) {
			this.itemCache_[this.getChildAt(numBlobs).getBlobRef()] = null;
			this.removeChildAt(numBlobs, true);
		}
	}
};

cam.BlobItemContainer.prototype.layout_ = function(force) {
	var el = this.getElement();
	var availWidth = el.clientWidth;

	if (!this.isVisible()) {
		return;
	}

	if (!force && !this.isLayoutDirty_ && availWidth == this.lastClientWidth_) {
		return;
	}

	this.isLayoutDirty_ = false;
	this.lastClientWidth_ = availWidth;

	var currentTop = this.constructor.BLOB_ITEM_MARGIN;
	var currentWidth = this.constructor.BLOB_ITEM_MARGIN;
	var rowStart = 0;
	var lastItem = this.getChildCount() - 1;

	for (var i = rowStart; i <= lastItem; i++) {
		var item = this.getChildAt(i);

		var nextWidth = currentWidth + this.thumbnailSize_ * item.getThumbAspect() + this.constructor.BLOB_ITEM_MARGIN;
		if (i != lastItem && nextWidth < availWidth) {
			currentWidth = nextWidth;
			continue;
		}

		// Decide how many items are going to be in this row. We choose the number that will result in the smallest adjustment to the image sizes having to be done.
		var rowEnd, rowWidth;
		if (i == lastItem) {
			rowEnd = lastItem;
			rowWidth = nextWidth;
			if (nextWidth / availWidth <
					this.constructor.LAST_ROW_CLOSE_ENOUGH_TO_FULL) {
				availWidth = nextWidth;
			}
		} else if (availWidth - currentWidth <= nextWidth - availWidth) {
			rowEnd = i - 1;
			rowWidth = currentWidth;
		} else {
			rowEnd = i;
			rowWidth = nextWidth;
		}

		currentTop += this.layoutRow_(rowStart, rowEnd, availWidth, rowWidth, currentTop) + this.constructor.BLOB_ITEM_MARGIN;

		currentWidth = this.constructor.BLOB_ITEM_MARGIN;
		rowStart = rowEnd + 1;
		i = rowEnd;
	}

	el.style.height = currentTop + this.constructor.BLOB_ITEM_MARGIN + 'px';
};

// @param {Number} startIndex The index of the first item in the row.
// @param {Number} endIndex The index of the last item in the row.
// @param {Number} availWidth The width available to the row for layout.
// @param {Number} usedWidth The width that the contents of the row consume
// using their initial dimensions, before any scaling or clipping.
// @param {Number} top The position of the top of the row.
// @return {Number} The height of the row after layout.
cam.BlobItemContainer.prototype.layoutRow_ = function(startIndex, endIndex, availWidth, usedWidth, top) {
	var currentLeft = 0;
	var rowHeight = Number.POSITIVE_INFINITY;

	var numItems = endIndex - startIndex + 1;
	var availThumbWidth = availWidth - (this.constructor.BLOB_ITEM_MARGIN * (numItems + 1));
	var usedThumbWidth = usedWidth - (this.constructor.BLOB_ITEM_MARGIN * (numItems + 1));

	for (var i = startIndex; i <= endIndex; i++) {
		var item = this.getChildAt(i);

		// We figure out the amount to adjust each item in this slightly non- intuitive way so that the adjustment is split up as fairly as possible. Figuring out a ratio up front and applying it to all items uniformly can end up with a large amount left over because of rounding.
		var numItemsLeft = (endIndex + 1) - i;
		var delta = Math.round((availThumbWidth - usedThumbWidth) / numItemsLeft);
		var originalWidth = this.thumbnailSize_ * item.getThumbAspect();
		var width = originalWidth + delta;
		var ratio = width / originalWidth;
		var height = Math.round(this.thumbnailSize_ * ratio);

		var elm = item.getElement();
		elm.style.left = currentLeft + this.constructor.BLOB_ITEM_MARGIN + 'px';
		elm.style.top = top + 'px';
		item.setSize(width, height);

		currentLeft += width + this.constructor.BLOB_ITEM_MARGIN;
		usedThumbWidth += delta;
		rowHeight = Math.min(rowHeight, height);
	}

	for (var i = startIndex; i <= endIndex; i++) {
		this.getChildAt(i).setHeight(rowHeight);
	}

	return rowHeight;
};

cam.BlobItemContainer.prototype.handleScroll_ = function() {
	if (!this.isVisible()) {
		return;
	}

	var docHeight = goog.dom.getDocumentHeight();
	var scroll = goog.dom.getDocumentScroll();
	var viewportSize = goog.dom.getViewportSize();

	if ((docHeight - scroll.y - viewportSize.height) >
			this.constructor.INFINITE_SCROLL_THRESHOLD_PX_) {
		return;
	}

	if (this.searchSession_) {
		this.searchSession_.loadMoreResults();
	}
};

cam.BlobItemContainer.prototype.findByBlobrefDone_ = function(permanode, result) {
	this.resetChildren_();
	if (!result) {
		return;
	}
	var meta = result.meta;
	if (!meta || !meta[permanode]) {
		return;
	}
	var item = new cam.BlobItem(permanode, meta);
	this.addChild(item, true);
};

// Clears all children from this container, reseting to the default state.
cam.BlobItemContainer.prototype.resetChildren_ = function() {
	this.removeChildren(true);
};

cam.BlobItemContainer.prototype.handleFileDrop_ = function(e) {
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
		// TODO(bslatkin): Add an uploading item placeholder while the upload is in progress. Somehow pipe through the POST progress.
		this.connection_.uploadFile(file, goog.bind(this.handleUploadSuccess_, this, file, recipient.blobRef_));
	}
};

cam.BlobItemContainer.prototype.handleUploadSuccess_ = function(file, recipient, blobRef) {
	this.connection_.createPermanode(
		goog.bind(this.handleCreatePermanodeSuccess_, this, file, recipient, blobRef));
};

cam.BlobItemContainer.prototype.handleCreatePermanodeSuccess_ = function(file, recipient, blobRef, permanode) {
	this.connection_.newSetAttributeClaim(permanode, 'camliContent', blobRef,
		goog.bind(this.handleSetAttributeSuccess_, this, file, recipient, blobRef, permanode));
};

cam.BlobItemContainer.prototype.handleSetAttributeSuccess_ = function(file, recipient, blobRef, permanode) {
	this.connection_.describeWithThumbnails(permanode, this.thumbnailSize_,
		goog.bind(this.handleDescribeSuccess_, this, recipient, permanode));
};

cam.BlobItemContainer.prototype.handleDescribeSuccess_ = function(recipient, permanode, describeResult) {
	if (recipient) {
		this.connection_.newAddAttributeClaim(recipient, 'camliMember', permanode);
	}

	if (this.searchSession_ && this.searchSession_.supportsChangeNotifications()) {
		// We'll find this when we reload.
		return;
	}

	var item = new cam.BlobItem(permanode, describeResult.meta);
	this.addChildAt(item, 0, true);
	if (!recipient) {
		return;
	}
};

cam.BlobItemContainer.prototype.handleFileDrag_ = function(e) {
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
		activeElement = this;
	}

	if (activeElement == this.dragActiveElement_) {
		return;
	}

	if (this.dragActiveElement_) {
		goog.dom.classes.remove(this.dragActiveElement_.getElement(), 'cam-dropactive');
	}

	this.dragActiveElement_ = activeElement;

	if (this.dragActiveElement_) {
		goog.dom.classes.add(this.dragActiveElement_.getElement(), 'cam-dropactive');
	}
};

cam.BlobItemContainer.prototype.hide_ = function() {
	goog.dom.classes.remove(this.getElement(), 'cam-blobitemcontainer-' + this.thumbnailSize_);
	goog.dom.classes.add(this.getElement(), 'cam-blobitemcontainer-hidden');
};

cam.BlobItemContainer.prototype.show_ = function() {
	goog.dom.classes.remove(this.getElement(), 'cam-blobitemcontainer-hidden');
	goog.dom.classes.add(this.getElement(), 'cam-blobitemcontainer-' + this.thumbnailSize_);
	this.layout_(true);
};
