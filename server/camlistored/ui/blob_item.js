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

goog.require('cam.permanodeUtils');
goog.require('cam.ServerType');
goog.require('cam.Thumber');


// BlobItem represents a blob item (blobRef) in a container. MetaBag is the meta
// map obtained in a describe response upon a search or describe query. It maps
// blobRefs to metadata for this blob and related blobs.
cam.BlobItem = function(blobRef, metaBag, opt_domHelper) {
	goog.base(this, null, null, opt_domHelper);

	this.update(blobRef, metaBag);

	this.setSupportedState(goog.ui.Component.State.CHECKED, true);
	this.setSupportedState(goog.ui.Component.State.DISABLED, true);
	this.setAutoStates(goog.ui.Component.State.CHECKED, false);

	// Blob items dispatch state when checked.
	this.setDispatchTransitionEvents(goog.ui.Component.State.CHECKED, true);
};
goog.inherits(cam.BlobItem, goog.ui.Control);

cam.BlobItem.prototype.update = function(blobRef, metaBag) {
	this.blobRef_ = blobRef;
	this.metaBag_ = metaBag;
	this.metaData_ = this.metaBag_[this.blobRef_];
	if (!this.metaData_) {
		return;
	}
	if (this.isImage()) {
		this.thumber_ = cam.Thumber.fromImageMeta(this.metaData_);
	} else {
		this.thumber_ = new cam.Thumber(this.metaData_.thumbnailSrc);
	}
};

cam.BlobItem.TITLE_HEIGHT = 21;

cam.BlobItem.prototype.getBlobRef = function() {
	return this.blobRef_;
};

cam.BlobItem.prototype.getThumbAspect = function() {
	if (!this.isImage()) {
		return 0;
	}
	if (!this.metaData_.image.width || !this.metaData_.image.height) {
		return 0;
	}
	return this.metaData_.image.width / this.metaData_.image.height;
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

	if (!this.thumber_) {
		return;
	}
	// It's important to only assign the new src if it has changed. Assigning a src causes layout and style recalc.
	var newThumb = this.thumber_.getSrc(adjustedHeight);
	if (newThumb != this.thumb_.getAttribute('src')) {
		this.thumb_.src = newThumb;
	}
};

cam.BlobItem.prototype.isImage = function() {
	if (!this.metaData_) {
		return false;
	}
	return Boolean(this.metaData_.image);
};

cam.BlobItem.prototype.camliType = function() {
	return this.metaData_ ? this.metaData_.camliType : "";
}

cam.BlobItem.prototype.isPermanode = function() {
	return Boolean(this.camliType() == 'permanode');
};

cam.BlobItem.prototype.isDir = function() {
	return Boolean(this.camliType() == 'directory');
};

cam.BlobItem.prototype.isFile = function() {
	return Boolean(this.camliType() == 'file');
};

cam.BlobItem.prototype.getLink_ = function() {
	if (!this.isPermanode()) {
		return './?b=' + this.blobRef_;
	}

	// The new detail page looks ridiculous for non-images, so don't go to it for those yet.
	var uri = new goog.Uri(location.href);
	uri.setParameterValue('p', this.blobRef_);
	if (this.isImage()) {
		uri.setParameterValue('newui', '1');
	}
	return uri.toString();
};

cam.BlobItem.prototype.getContent_ = function() {
	if (!this.metaData_) {
		return '';
	}
	var metaData = this.metaData_;
	if (metaData.camliType == 'permanode' &&
		!!metaData.permanode &&
		!!metaData.permanode.attr) {
		var content = cam.permanodeUtils.getSingleAttr(metaData.permanode, 'camliContent');
		if (content) {
			return content;
		}
		content = cam.permanodeUtils.getSingleAttr(metaData.permanode, 'camliContentImage');
		if (content) {
			return content;
		}
	}
	return '';
};

cam.BlobItem.prototype.getTitle_ = function() {
	if (!!this.title_) {
		return this.title_;
	}
	if (!this.metaData_) {
		return '';
	}
	var metaData = this.metaData_;
	if (metaData.camliType == 'permanode') {
		if (!!metaData.permanode &&
			!!metaData.permanode.attr) {
			var title = cam.permanodeUtils.getSingleAttr(metaData.permanode, 'title');
			if (title) {
				this.title_ = metaData.permanode.attr.title;
				return this.title_;
			}
		}
		var content = this.getContent_();
		if (content != '') {
			var contentTitle = this.getTitleFrom_(content);
			if (contentTitle != '') {
				this.title_ = contentTitle;
				return this.title_;
			}
		}
		// TODO(mpl): add call to getSpecialTitle_ that would
		// return the twitterId if a twitter.com:tweet for example.
		this.title_ = this.blobRef_;
		return this.title_;
	}
	if (metaData.camliType == 'file' &&
		!!metaData.file) {
		this.title_ = metaData.file.fileName;
		return this.title_;
	}
	if (metaData.camliType == 'directory' &&
		!!metaData.dir) {
		this.title_ = metaData.dir.fileName;
		return this.title_;
	}
	return '';
};

cam.BlobItem.prototype.getTitleFrom_ = function(br) {
	// Probably overkill in resources usage but at least it's a
	// readable recursion instead of the resolvedMetaData mess.
	// TODO(mpl): if need be, we could cache this child as
	// this.contentChild_ or something.
	var child = new cam.BlobItem(br, this.metaBag_);
	var title = child.getTitle_();
	return child.getTitle_();
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
