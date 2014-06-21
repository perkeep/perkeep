/*
Copyright 2013 The Camlistore Authors

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

goog.provide('cam.BlobItem');

goog.require('goog.dom');
goog.require('goog.dom.classes');
goog.require('goog.events.EventHandler');
goog.require('goog.events.EventType');
goog.require('goog.ui.Control');

goog.require('cam.ServerType');
goog.require('cam.Thumber');


// @fileoverview An item showing in a blob item container; represents a blob that has already been uploaded in the system, or acts as a placeholder for a new blob.
// @param {string} blobRef BlobRef for the item.
// @param {cam.ServerType.IndexerMetaBag} metaBag Maps blobRefs to metadata for this blob and related blobs.
// @param {string} opt_contentLink if "true", use the contained file blob as link when decorating
// @param {goog.dom.DomHelper=} opt_domHelper DOM helper to use.
// @extends {goog.ui.Control}
// @constructor
cam.BlobItem = function(blobRef, metaBag, opt_contentLink, opt_domHelper) {
	goog.base(this, null, null, opt_domHelper);

	this.update(blobRef, metaBag, opt_contentLink);

	this.setSupportedState(goog.ui.Component.State.CHECKED, true);
	this.setSupportedState(goog.ui.Component.State.DISABLED, true);
	this.setAutoStates(goog.ui.Component.State.CHECKED, false);

	// Blob items dispatch state when checked.
	this.setDispatchTransitionEvents(goog.ui.Component.State.CHECKED, true);
};
goog.inherits(cam.BlobItem, goog.ui.Control);

cam.BlobItem.prototype.update = function(blobRef, metaBag, opt_contentLink) {
	// TODO(mpl): Hack so we know when to decorate with the blobref of the contained file, instead of with the permanode, as the link. Idiomatic alternative suggestion very welcome.

	this.useContentAsLink_ = "false";
	if (typeof opt_contentLink !== "undefined" && opt_contentLink == "true") {
		this.useContentAsLink_ = opt_contentLink;
	}

	this.blobRef_ = blobRef;
	this.metaBag_ = metaBag;
	this.metaData_ = this.metaBag_[this.blobRef_];
	this.resolvedMetaData_ = cam.BlobItem.resolve(this.blobRef_, this.metaBag_);

	if (this.resolvedMetaData_ && this.resolvedMetaData_.image) {
		this.thumber_ = cam.Thumber.fromImageMeta(this.resolvedMetaData_);
	} else {
		this.thumber_ = new cam.Thumber(this.metaData_.thumbnailSrc);
	}
};

cam.BlobItem.TITLE_HEIGHT = 21;

// TODO(bslatkin): Handle more permanode types.
// @param {string} blobRef string BlobRef to resolve.
// @param {cam.ServerType.IndexerMetaBag} metaBag Metadata bag to use for resolving the blobref.
// @return {cam.ServerType.IndexerMeta?}
cam.BlobItem.resolve = function(blobRef, metaBag) {
	var metaData = metaBag[blobRef];
	if (metaData.camliType == 'permanode' && metaData.permanode && metaData.permanode.attr) {
		if (metaData.permanode.attr.camliContent) {
			// Permanode is pointing at another blob.
			var content = metaData.permanode.attr.camliContent;
			if (content.length == 1) {
				return metaBag[content[0]];
			}
		} else {
			// Permanode is its own content.
			return metaData;
		}
	}
	return null;
};

cam.BlobItem.prototype.isCollection = function() {
	// TODO(mpl): for now disallow being a collection if it
	// has members. What else to check?
	if (!this.resolvedMetaData_ || this.resolvedMetaData_.camliType != 'permanode' || !this.resolvedMetaData_.permanode || !this.resolvedMetaData_.permanode.attr || this.resolvedMetaData_.permanode.attr.camliContent) {
			return false;
	}
	return true;
};

cam.BlobItem.prototype.getBlobRef = function() {
	return this.blobRef_;
};

cam.BlobItem.prototype.getThumbAspect = function() {
	if (!this.metaData_.thumbnailWidth || !this.metaData_.thumbnailHeight) {
		return 0;
	}
	return this.metaData_.thumbnailWidth / this.metaData_.thumbnailHeight;
};

cam.BlobItem.prototype.getWidth = function() {
	return parseInt(this.getElement().style.width);
};

cam.BlobItem.prototype.getHeight = function() {
	return parseInt(this.getElement().style.height);
};

cam.BlobItem.prototype.setWidth = function(w) {
	this.setSize(w, this.getHeight());
};

cam.BlobItem.prototype.setHeight = function(h) {
	this.setSize(this.getWidth(), h);
};

// Sets the display size of the item. The thumbnail will be scaled, centered,
// and clipped within this size as appropriate.
// @param {number} w
// @param {number} h
cam.BlobItem.prototype.setSize = function(w, h) {
	this.getElement().style.width = w + 'px';
	this.getElement().style.height = h + 'px';

	var thumbHeight = h;
	if (!this.isImage()) {
		thumbHeight -= this.constructor.TITLE_HEIGHT;
	}
	this.setThumbSize(w, thumbHeight);
};

// Sets the display size of just the thumbnail. It will be scaled, centered, and
// clipped within this size as appropriate.
// @param {number} w
// @param {number} h
cam.BlobItem.prototype.setThumbSize = function(w, h) {
	// In the case of images, we want a full bleed to both w and h, so we clip the bigger dimension as necessary. It's not easy to notice that a few pixels have been shaved off the edge of a photo.
	// In the case of non-images, we have an icon with text underneath, so we cannot clip. Instead, just constrain the icon to fit the available space.
	var adjustedHeight;
	if (this.isImage()) {
		adjustedHeight = this.getThumbAspect() < w / h ? w / this.getThumbAspect() : h;
	} else {
		adjustedHeight = this.getThumbAspect() < w / h ? h : w / this.getThumbAspect();
	}
	var adjustedWidth = adjustedHeight * this.getThumbAspect();

	this.thumb_.width = Math.round(adjustedWidth);
	this.thumb_.height = Math.round(adjustedHeight);

	this.thumbClip_.style.width = w + 'px';
	this.thumbClip_.style.height = h + 'px';

	this.thumb_.style.top = Math.round((h - adjustedHeight) / 2) + 'px';
	this.thumb_.style.left = Math.round((w - adjustedWidth) / 2) + 'px';

	this.loading_.style.top = Math.round((h - 85) / 2) + 'px';
	this.loading_.style.left = Math.round((w - 70) / 2) + 'px';

	// It's important to only assign the new src if it has changed. Assigning a src causes layout and style recalc.
	var newThumb = this.thumber_.getSrc(adjustedHeight);
	if (newThumb != this.thumb_.getAttribute('src')) {
		this.thumb_.src = newThumb;
	}
};

cam.BlobItem.prototype.isImage = function() {
	return Boolean(this.resolvedMetaData_.image);
};

cam.BlobItem.prototype.getLink_ = function() {
	if (this.useContentAsLink_ == "true") {
		var b = this.getFileBlobref_();
		if (b == "") {
			b = this.getDirBlobref_();
		}
		return './?b=' + b;
	}

	// The new detail page looks ridiculous for non-images, so don't go to it for those yet.
	var uri = new goog.Uri(location.href);
	uri.setParameterValue('p', this.blobRef_);
	if (this.isImage()) {
		uri.setParameterValue('newui', '1');
	}
	return uri.toString();
};

cam.BlobItem.prototype.getFileBlobref_ = function() {
	if (this.resolvedMetaData_ && this.resolvedMetaData_.camliType == 'file') {
		return this.resolvedMetaData_.blobRef;
	}
	return "";
}

cam.BlobItem.prototype.getDirBlobref_ = function() {
	if (this.resolvedMetaData_ && this.resolvedMetaData_.camliType == 'directory') {
		return this.resolvedMetaData_.blobRef;
	}
	return "";
}

cam.BlobItem.prototype.getTitle_ = function() {
	if (this.metaData_) {
		if (this.metaData_.camliType == 'permanode' &&
			!!this.metaData_.permanode &&
			!!this.metaData_.permanode.attr &&
			!!this.metaData_.permanode.attr.title) {
			return this.metaData_.permanode.attr.title;
		}
	}
	if (this.resolvedMetaData_) {
		if (this.resolvedMetaData_.camliType == 'file' &&
			!!this.resolvedMetaData_.file) {
			return this.resolvedMetaData_.file.fileName;
		}
		if (this.resolvedMetaData_.camliType == 'directory' &&
					!!this.resolvedMetaData_.dir) {
				return this.resolvedMetaData_.dir.fileName;
			}
		if (this.resolvedMetaData_.camliType == 'permanode' &&
			!!this.resolvedMetaData_.permanode &&
			!!this.resolvedMetaData_.permanode.attr &&
			!!this.resolvedMetaData_.permanode.attr.title) {
			return this.resolvedMetaData_.permanode.attr.title;
		}
	}
	return 'Unknown title';
};

cam.BlobItem.prototype.createDom = function() {
	this.decorateInternal(this.dom_.createElement('div'));
};

cam.BlobItem.prototype.decorateInternal = function(element) {
	cam.BlobItem.superClass_.decorateInternal.call(this, element);

	var el = this.getElement();
	goog.dom.classes.add(el, 'cam-blobitem');

	this.link_ = this.dom_.createDom('a');

	this.thumbClip_ = this.dom_.createDom('div', 'cam-blobitem-thumbclip cam-blobitem-loading');
	this.link_.appendChild(this.thumbClip_);

	this.loading_ = this.dom_.createDom('div', 'cam-blobitem-progress',
		this.dom_.createDom('div', 'lefttop'),
		this.dom_.createDom('div', 'leftbottom'),
		this.dom_.createDom('div', 'righttop'),
		this.dom_.createDom('div', 'rightbottom'));
	this.thumbClip_.appendChild(this.loading_);

	this.thumb_ = this.dom_.createDom('img', 'cam-blobitem-thumb');
	this.thumb_.onload = function(e){
		goog.dom.removeNode(this.loading_);
		goog.dom.classes.remove(this.thumbClip_, 'cam-blobitem-loading');
	}.bind(this);
	this.thumbClip_.appendChild(this.thumb_);

	el.appendChild(this.link_);

	this.checkmark_ = this.dom_.createDom('div', 'checkmark');
	this.getElement().appendChild(this.checkmark_);

	this.label_ = this.dom_.createDom('span', 'cam-blobitem-thumbtitle');
	this.link_.appendChild(this.label_);

	this.updateDom();

	this.getElement().addEventListener('click', this.handleClick_.bind(this));
	this.setEnabled(false);
};

// The image src is not set here because that depends on layout. Instead, it
// gets set as a side-effect of BlobItemContainer.prototype.layout().
cam.BlobItem.prototype.updateDom = function() {
	this.link_.href = this.getLink_();

	if (this.isImage()) {
		this.addClassName('cam-blobitem-image');
		this.thumb_.title = this.getTitle_();
		this.label_.textContent = '';
	} else {
		this.removeClassName('cam-blobitem-image');
		this.label_.textContent = this.getTitle_();
	}
};

cam.BlobItem.prototype.handleClick_ = function(e) {
	if (!this.checkmark_) {
		return;
	}

	if (e.target == this.checkmark_ || this.checkmark_.contains(e.target)) {
		this.setChecked(!this.isChecked());
		e.preventDefault();
	}
};
