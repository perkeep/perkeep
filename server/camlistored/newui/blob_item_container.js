/**
 * @fileoverview TODO
 *
 */
goog.provide('camlistore.BlobItemContainer');
goog.provide('camlistore.BlobItemContainer.EventType');

goog.require('goog.dom');
goog.require('goog.dom.classes');
goog.require('goog.events.Event');
goog.require('goog.events.EventHandler');
goog.require('goog.events.EventType');
goog.require('goog.ui.Container');
goog.require('camlistore.BlobItem');
goog.require('camlistore.CreateItem');


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
   * @type {camlistore.ServerConnection}
   * @private
   */
  this.connection_ = connection;

  /**
   * @type {goog.events.EventHandler}
   * @private
   */
  this.eh_ = new goog.events.EventHandler(this);
};
goog.inherits(camlistore.BlobItemContainer, goog.ui.Container);


camlistore.BlobItemContainer.prototype.hasCreateItem_ = false;

camlistore.BlobItemContainer.prototype.setHasCreateItem = function(v) {
  this.hasCreateItem_ = v;
};


/**
 * @enum {string}
 */
camlistore.BlobItemContainer.EventType = {
  SHOW_RECENT: 'Camlistore_BlobInfoContainer_ShowRecent'
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
  this.eh_.listen(
      this, camlistore.BlobItemContainer.EventType.SHOW_RECENT,
      this.showRecent_);
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
camlistore.BlobItemContainer.prototype.showRecent_ = function() {
  this.connection_.getRecentlyUpdatedPermanodes(
      goog.bind(this.showRecentDone_, this),
      100);  // TODO(bslatkin): Use instance variable for thumbnail size
};


/**
 * @param {Object} result JSON response to this request.
 */
camlistore.BlobItemContainer.prototype.showRecentDone_ = function(result) {
  this.resetChildren_();
  for (var i = 0, n = result.recent.length; i < n; i++) {
    var blobRef = result.recent[i].blobref;
    var item = new camlistore.BlobItem(blobRef, result);
    this.addChild(item, true);
  }
};


camlistore.BlobItemContainer.prototype.resetChildren_ = function() {
  this.removeChildren(true);
  if (this.hasCreateItem_) {
    var createItem = new camlistore.CreateItem();
    this.addChild(createItem, true);
    this.eh_.listen(
      createItem.getElement(), goog.events.EventType.CLICK,
      function() {
        console.log('Clicked');
      });
  }
}
