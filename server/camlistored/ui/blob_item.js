/**
 * @fileoverview An item showing in a blob item container; represents a blob
 * that has already been uploaded in the system, or acts as a placeholder
 * for a new blob.
 *
 */
goog.provide('camlistore.BlobItem');

goog.require('camlistore.ServerType');
goog.require('goog.dom');
goog.require('goog.dom.classes');
goog.require('goog.events.EventHandler');
goog.require('goog.events.EventType');
goog.require('goog.ui.Control');



/**
 * @param {string} blobRef BlobRef for the item.
 * @param {camlistore.ServerType.IndexerMetaBag} metaBag Maps blobRefs to
 *   metadata for this blob and related blobs.
 * @param {string} opt_contentLink if "true", use the contained file blob as link when decorating
 * @param {goog.dom.DomHelper=} opt_domHelper DOM helper to use.
 *
 * @extends {goog.ui.Control}
 * @constructor
 */
camlistore.BlobItem = function(blobRef, metaBag, opt_contentLink, opt_domHelper) {
  goog.base(this, null, null, opt_domHelper);

  // TODO(mpl): Hack so we know when to decorate with the blobref
  // of the contained file, instead of with the permanode, as the link.
  // Idiomatic alternative suggestion very welcome.
  /**
   * @type {string}
   * @private
   */
  this.useContentAsLink_ = "false";
  if (typeof opt_contentLink !== "undefined" && opt_contentLink == "true") {
    this.useContentAsLink_ = opt_contentLink;
  }

  /**
   * @type {string}
   * @private
   */
  this.blobRef_ = blobRef;

  /**
   * @type {camlistore.ServerType.IndexerMetaBag}
   * @private
   */
  this.metaBag_ = metaBag;

  /**
   * Metadata for the blobref this item represents.
   * @type {camlistore.ServerType.IndexerMeta}
   * @private
   */
  this.metaData_ = this.metaBag_[this.blobRef_];

  /**
   * Metadata for the underlying blobref for this item; for example, this
   * would be the blobref that is currently the content for the permanode
   * specified by 'blobRef'.
   *
   * @type {camlistore.ServerType.IndexerMeta?}
   * @private
   */
  this.resolvedMetaData_ = camlistore.BlobItem.resolve(
      this.blobRef_, this.metaBag_);

  this.setSupportedState(goog.ui.Component.State.CHECKED, true);
  this.setSupportedState(goog.ui.Component.State.DISABLED, true);
  this.setAutoStates(goog.ui.Component.State.CHECKED, false);

  // Blob items dispatch state when checked.
  this.setDispatchTransitionEvents(
      goog.ui.Component.State.CHECKED,
      true);
};
goog.inherits(camlistore.BlobItem, goog.ui.Control);


/**
 * Amount of space to reserve vertically for the title, in the cases it is
 * shown.
 */
camlistore.BlobItem.TITLE_HEIGHT = 21;


/**
 * TODO(bslatkin): Handle more permanode types.
 *
 * @param {string} blobRef string BlobRef to resolve.
 * @param {camlistore.ServerType.IndexerMetaBag} metaBag Metadata bag to use
 *   for resolving the blobref.
 * @return {camlistore.ServerType.IndexerMeta?}
 */
camlistore.BlobItem.resolve = function(blobRef, metaBag) {
  var metaData = metaBag[blobRef];
  if (metaData.camliType == 'permanode' &&
      !!metaData.permanode &&
      !!metaData.permanode.attr) {
    if (!!metaData.permanode.attr.camliContent) {
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

/**
 * @return {boolean}
 */
camlistore.BlobItem.prototype.isCollection = function() {
	// TODO(mpl): for now disallow being a collection if it
	// has members. What else to check?
	if (!this.resolvedMetaData_ ||
		this.resolvedMetaData_.camliType != 'permanode' ||
		!this.resolvedMetaData_.permanode ||
		!this.resolvedMetaData_.permanode.attr ||
		!!this.resolvedMetaData_.permanode.attr.camliContent) {
			return false;
	}
	return true;
};


/**
 * @return {string}
 */
camlistore.BlobItem.prototype.getBlobRef = function() {
  return this.blobRef_;
};


/**
 * Gets the aspect ratio (w/h) of the thumbnail.
 * @return {number}
 */
camlistore.BlobItem.prototype.getThumbAspect = function() {
  if (!this.metaData_.thumbnailWidth || !this.metaData_.thumbnailHeight) {
    return 0;
  }
  return this.metaData_.thumbnailWidth / this.metaData_.thumbnailHeight;
};


/**
 * @return {number}
 */
camlistore.BlobItem.prototype.getWidth = function() {
  return parseInt(this.getElement().style.width);
};


/**
 * @return {number}
 */
camlistore.BlobItem.prototype.getHeight = function() {
  return parseInt(this.getElement().style.height);
};


/**
 * @param {number} w
 */
camlistore.BlobItem.prototype.setWidth = function(w) {
  this.setSize(w, this.getHeight());
};


/**
 * @param {number} h
 */
camlistore.BlobItem.prototype.setHeight = function(h) {
  this.setSize(this.getWidth(), h);
};


/**
 * Sets the display size of the item. The thumbnail will be scaled, centered,
 * and clipped within this size as appropriate.
 * @param {number} w
 * @param {number} h
 */
camlistore.BlobItem.prototype.setSize = function(w, h) {
  this.getElement().style.width = w + 'px';
  this.getElement().style.height = h + 'px';

  var thumbHeight = h;
  if (!this.isImage()) {
    thumbHeight -= this.constructor.TITLE_HEIGHT;
  }
  this.setThumbSize(w, thumbHeight);
};


/**
 * Sets the display size of just the thumbnail. It will be scaled, centered, and
 * clipped within this size as appropriate.
 * @param {number} w
 * @param {number} h
 */
camlistore.BlobItem.prototype.setThumbSize = function(w, h) {
  // In the case of images, we don't want a full bleed to both w and h, so we
  // clip the bigger dimension as necessary. It's not easy to notice that a few
  // pixels have been shaved off the edge of a photo.
  //
  // In the case of non-images, we have an icon with text underneath, so we
  // cannot clip. Instead, just constrain the icon to fit the available space.
  var adjustedHeight;
  if (this.isImage()) {
    adjustedHeight = this.getThumbAspect() < w / h ?
      w / this.getThumbAspect() : h;
  } else {
    adjustedHeight = this.getThumbAspect() < w / h ?
      h : w / this.getThumbAspect();
  }
  var adjustedWidth = adjustedHeight * this.getThumbAspect();

  this.thumb_.width = Math.round(adjustedWidth);
  this.thumb_.height = Math.round(adjustedHeight);

  this.thumbClip_.style.width = w + 'px';
  this.thumbClip_.style.height = h + 'px';

  this.thumb_.style.top = Math.round((h - adjustedHeight) / 2) + 'px';
  this.thumb_.style.left = Math.round((w - adjustedWidth) / 2) + 'px';

  // Load a differently sized image from server if necessary.
  if (!this.thumb_.src || adjustedWidth > this.thumb_.width ||
      adjustedHeight > this.thumb_.height) {
    // Round the height up to the nearest 20% to increase the probability of
    // cache hits.
    var rh = Math.ceil(adjustedHeight / 5) * 5;

    // TODO(aa): This is kind of a hack, it would be better if the server just
    // returned the base URL and the aspect ratio, rather than specific
    // dimensions.
    this.thumb_.src = this.getThumbSrc_().split('?')[0] + '?mh=' + rh;
  }
};


/**
 * Determine whether the blob is a permanode for an image.
 * @return {boolean}
 */
camlistore.BlobItem.prototype.isImage = function() {
  return Boolean(this.resolvedMetaData_.image);
};


/**
 * @return {string}
 */
camlistore.BlobItem.prototype.getThumbSrc_ = function() {
  return './' + this.metaData_.thumbnailSrc;
};


/**
 * @return {string}
 */
camlistore.BlobItem.prototype.getLink_ = function() {
  if (this.useContentAsLink_ == "true") {
    var b = this.getFileBlobref_();
    if (b == "") {
      b = this.getDirBlobref_();
    }
    return './?b=' + b;
  }
  return './?p=' + this.blobRef_;
};


/**
 * @private
 * @return {string}
 */
camlistore.BlobItem.prototype.getFileBlobref_ = function() {
	if (this.resolvedMetaData_ &&
		this.resolvedMetaData_.camliType == 'file') {
		return this.resolvedMetaData_.blobRef;
	}
	return "";
}

/**
 * @private
 * @return {string}
 */
camlistore.BlobItem.prototype.getDirBlobref_ = function() {
	if (this.resolvedMetaData_ &&
		this.resolvedMetaData_.camliType == 'directory') {
		return this.resolvedMetaData_.blobRef;
	}
	return "";
}

/**
 * @return {string}
 */
camlistore.BlobItem.prototype.getTitle_ = function() {
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


/**
 * Creates an initial DOM representation for the component.
 */
camlistore.BlobItem.prototype.createDom = function() {
  this.decorateInternal(this.dom_.createElement('div'));
};


/**
 * Decorates an existing HTML DIV element.
 * @param {Element} element The DIV element to decorate.
 */
camlistore.BlobItem.prototype.decorateInternal = function(element) {
  camlistore.BlobItem.superClass_.decorateInternal.call(this, element);

  var el = this.getElement();
  goog.dom.classes.add(el, 'cam-blobitem');

  var link = this.dom_.createDom('a');
  link.href = this.getLink_();

  this.thumbClip_ = this.dom_.createDom('div', 'cam-blobitem-thumbclip');
  link.appendChild(this.thumbClip_);

  this.thumb_ = this.dom_.createDom('img', 'cam-blobitem-thumb');
  this.thumbClip_.appendChild(this.thumb_);

  el.appendChild(link);

  this.checkmark_ = this.dom_.createDom('div', 'checkmark');
  this.getElement().appendChild(this.checkmark_);

  if (!this.isImage()) {
    var label = this.dom_.createDom('span', 'cam-blobitem-thumbtitle');
    label.appendChild(document.createTextNode(this.getTitle_()));
    link.appendChild(label);
  }

  this.getElement().addEventListener('click', this.handleClick_.bind(this));
  this.setEnabled(false);
};

/**
 * @param {goog.events.Event} e The drag drop event.
 * @private
 */
camlistore.BlobItem.prototype.handleClick_ = function(e) {
  if (!this.checkmark_) {
    return;
  }

  if (e.target == this.checkmark_ || this.checkmark_.contains(e.target)) {
    this.setChecked(!this.isChecked());
    e.preventDefault();
  }
};
