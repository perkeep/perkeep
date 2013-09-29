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
 * Sets the horizontal width that the item consumes in BlobItemContainer. The
 * thumbnail is centered within this space. If the frame is less than the width
 * of the thumbnail, then the thumbnail is clipped horizontally to fit.
 *
 * @param {number} The width of the frame.
 */
camlistore.BlobItem.prototype.setFrameWidth = function(w) {
  var el = this.getElement();
  el.style.width = w + 'px';

  var offset = (w - this.getThumbWidth()) / 2;
  var thumbEl = this.dom_.getElementByClass('cam-blobitem-thumb', el);
  thumbEl.style.left = offset + 'px';
};


/**
 * Resets the frame to the width of the thumbnail.
 */
camlistore.BlobItem.prototype.resetFrameWidth = function() {
  this.setFrameWidth(this.getThumbWidth());
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
 * @return {number}
 */
camlistore.BlobItem.prototype.getThumbHeight_ = function() {
  return this.metaData_.thumbnailHeight || 0;
};


/**
 * @return {number}
 */
camlistore.BlobItem.prototype.getThumbWidth = function() {
  return this.metaData_.thumbnailWidth || 0;
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

  var link = this.dom_.createDom('a', 'cam-blobitem-thumb');
  link.href = this.getLink_();

  var thumb = this.dom_.createDom('img');
  thumb.src = this.getThumbSrc_();
  thumb.height = this.getThumbHeight_();
  thumb.width = this.getThumbWidth();
  link.appendChild(thumb);

  el.appendChild(link);
  this.loadCheckmark_();

  if (!this.isImage()) {
    goog.dom.classes.add(el, 'cam-blobitem-notimage');
    var label = this.dom_.createDom('a', 'cam-blobitem-thumbtitle');
    this.dom_.setTextContent(label, this.getTitle_());
    el.appendChild(label);
  }

  this.getElement().addEventListener('click', this.handleClick_.bind(this));
  this.setEnabled(false);
};

/**
 * @private
 */
camlistore.BlobItem.prototype.loadCheckmark_ = function() {
  var req = new XMLHttpRequest();
  req.open("GET", 'checkmark.svg', true);
  req.onload = goog.bind(function() {
    var temp = document.createElement('div');
    temp.innerHTML = req.responseText;
    this.checkmark_ = temp.getElementsByTagName('svg')[0];
    this.checkmark_.setAttribute('class', 'checkmark');
    this.getElement().appendChild(this.checkmark_);
  }, this);
  req.send(null);
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
