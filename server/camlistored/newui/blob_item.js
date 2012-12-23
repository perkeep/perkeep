/**
 * @fileoverview TODO
 *
 */
goog.provide('camlistore.BlobItem');

goog.require('goog.dom');
goog.require('goog.dom.classes');
goog.require('goog.events.EventHandler');
goog.require('goog.events.EventType');
goog.require('goog.ui.Component');



/**
 * @param {string} blobRef BlobRef for the item.
 * @param {camlistore.ServerType.IndexerMetaBag} metaBag Maps blobRefs to
 *   metadata for this blob and related blobs.
 * @param {goog.dom.DomHelper=} opt_domHelper DOM helper to use.
 *
 * @extends {goog.ui.Component}
 * @constructor
 */
camlistore.BlobItem = function(blobRef, metaBag, opt_domHelper) {
  goog.base(this, opt_domHelper);

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
   * @type {camlistore.ServerType.IndexerMeta}
   * @private
   */
  this.resolvedMetaData_ = camlistore.BlobItem.resolve(
      this.blobRef_, this.metaBag_);

  /**
   * @type {goog.events.EventHandler}
   * @private
   */
  this.eh_ = new goog.events.EventHandler(this);
};
goog.inherits(camlistore.BlobItem, goog.ui.Component);


/**
 * TODO: Handle more permanode types.
 *
 * @param {string} blobRef string BlobRef to resolve.
 * @param {camlistore.ServerType.IndexerMetaBag} metaBag Metadata bag to use
 *   for resolving the blobref.
 * @return {camlistore.ServerType.IndexerMeta}
 */
camlistore.BlobItem.resolve = function(blobRef, metaBag) {
  var metaData = metaBag[blobRef];
  if (metaData.camliType == 'permanode' && !!metaData.permanode &&
      !!metaData.permanode.attr && !!metaData.permanode.attr.camliContent) {
    var content = metaData.permanode.attr.camliContent;
    if (content.length == 1) {
      return metaBag[content[0]];
    }
  }

  return null;

};


/**
 * @return {string}
 */
camlistore.BlobItem.prototype.getThumbSrc_ = function() {
  // TODO(bslatkin): Make this prefix configured by globals, discovered by
  // the page at initialization.
  return '../' + this.metaData_.thumbnailSrc;
};


/**
 * @return {number}
 */
camlistore.BlobItem.prototype.getThumbHeight_ = function() {
  return this.metaData_.thumbnailHeight;
};


/**
 * @return {number}
 */
camlistore.BlobItem.prototype.getThumbWidth_ = function() {
  return this.metaData_.thumbnailWidth;
};


/**
 * @return {string}
 */
camlistore.BlobItem.prototype.getTitle_ = function() {
  if (this.resolvedMetaData_ && this.resolvedMetaData_.camliType == 'file' &&
      !!this.resolvedMetaData_.file) {
    return this.resolvedMetaData_.file.fileName;
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
  goog.dom.classes.add(el, 'cam-blobitem', 'cam-blobitem-150');

  var thumbEl = this.dom_.createDom('img', 'cam-blobitem-thumb');
  thumbEl.src = this.getThumbSrc_();
  thumbEl.height = this.getThumbHeight_();
  thumbEl.width = this.getThumbWidth_();
  this.dom_.appendChild(el, thumbEl);

  var titleEl = this.dom_.createDom('p', 'cam-blobitem-thumbtitle');
  this.dom_.setTextContent(titleEl, this.getTitle_());
  this.dom_.appendChild(el, titleEl);
};


/** @override */
camlistore.BlobItem.prototype.disposeInternal = function() {
  camlistore.BlobItem.superClass_.disposeInternal.call(this);
  this.eh_.dispose();
};


/**
 * Called when component's element is known to be in the document.
 */
camlistore.BlobItem.prototype.enterDocument = function() {
  camlistore.BlobItem.superClass_.enterDocument.call(this);
  // Add event handlers here
};


/**
 * Called when component's element is known to have been removed from the
 * document.
 */
camlistore.BlobItem.prototype.exitDocument = function() {
  camlistore.BlobItem.superClass_.exitDocument.call(this);
  // Clear event handlers here
};
