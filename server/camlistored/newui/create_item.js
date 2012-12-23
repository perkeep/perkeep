/**
 * @fileoverview TODO
 *
 */
goog.provide('camlistore.CreateItem');

goog.require('goog.dom');
goog.require('goog.dom.classes');
goog.require('goog.events.EventHandler');
goog.require('goog.events.EventType');
goog.require('goog.ui.Control');


/**
 * @param {string} blobRef BlobRef for the item.
 * @param {camlistore.ServerType.IndexerMetaBag} metaBag Maps blobRefs to
 *   metadata for this blob and related blobs.
 * @param {goog.dom.DomHelper=} opt_domHelper DOM helper to use.
 *
 * @extends {goog.ui.Component}
 * @constructor
 */
camlistore.CreateItem = function(opt_domHelper) {
  goog.base(this, opt_domHelper);

  /**
   * @type {goog.events.EventHandler}
   * @private
   */
  this.eh_ = new goog.events.EventHandler(this);
};
goog.inherits(camlistore.CreateItem, goog.ui.Control);


/**
 * Creates an initial DOM representation for the component.
 */
camlistore.CreateItem.prototype.createDom = function() {
  this.decorateInternal(this.dom_.createElement('div'));
};


/**
 * Decorates an existing HTML DIV element.
 * @param {Element} element The DIV element to decorate.
 */
camlistore.CreateItem.prototype.decorateInternal = function(element) {
  camlistore.CreateItem.superClass_.decorateInternal.call(this, element);

  var el = this.getElement();
  goog.dom.classes.add(el, 'cam-blobitem', 'cam-blobitem-150');

  var plusEl = this.dom_.createDom('a', 'cam-createitem-link');
  this.dom_.setTextContent(plusEl, "+")
  this.dom_.appendChild(el, plusEl);

  var titleEl = this.dom_.createDom('p', 'cam-createitem-thumbtitle');
  this.dom_.setTextContent(titleEl, "Drag & drop files or click");
  this.dom_.appendChild(el, titleEl);
};


/** @override */
camlistore.CreateItem.prototype.disposeInternal = function() {
  camlistore.CreateItem.superClass_.disposeInternal.call(this);
  this.eh_.dispose();
};


/**
 * Called when component's element is known to be in the document.
 */
camlistore.CreateItem.prototype.enterDocument = function() {
  camlistore.CreateItem.superClass_.enterDocument.call(this);
  // Add event handlers here
};


/**
 * Called when component's element is known to have been removed from the
 * document.
 */
camlistore.CreateItem.prototype.exitDocument = function() {
  camlistore.CreateItem.superClass_.exitDocument.call(this);
  // Clear event handlers here
};
