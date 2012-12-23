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
 * @param {goog.dom.DomHelper=} opt_domHelper DOM helper to use.
 *
 * @extends {goog.ui.Component}
 * @constructor
 */
camlistore.BlobItem = function(opt_domHelper) {
  goog.base(this, opt_domHelper);

  /**
   * @type {goog.events.EventHandler}
   * @private
   */
  this.eh_ = new goog.events.EventHandler(this);
};
goog.inherits(camlistore.BlobItem, goog.ui.Component);


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

  var elem = this.getElement();
  goog.dom.classes.add(elem, 'cam-my-class');
  goog.dom.setTextContent(elem, 'Woot!');
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
};


/**
 * Called when component's element is known to have been removed from the
 * document.
 */
camlistore.BlobItem.prototype.exitDocument = function() {
  camlistore.BlobItem.superClass_.exitDocument.call(this);
};
