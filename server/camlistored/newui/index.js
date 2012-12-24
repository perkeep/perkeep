/**
 * @fileoverview TODO
 *
 */
goog.provide('camlistore.IndexPage');

goog.require('goog.dom');
goog.require('goog.dom.classes');
goog.require('goog.events.EventHandler');
goog.require('goog.events.EventType');
goog.require('goog.ui.Component');
goog.require('camlistore.BlobItemContainer');
goog.require('camlistore.BlobItemContainer.EventType');
goog.require('camlistore.ServerConnection');
goog.require('camlistore.Toolbar');
goog.require('camlistore.Toolbar.EventType');


/**
 * @param {camlistore.ServerType.DiscoveryDocument} config Global config
 *   of the current server this page is being rendered for.
 * @param {goog.dom.DomHelper=} opt_domHelper DOM helper to use.
 *
 * @extends {goog.ui.Component}
 * @constructor
 */
camlistore.IndexPage = function(config, opt_domHelper) {
  goog.base(this, opt_domHelper);

  /**
   * @type {Object}
   * @private
   */
  this.config_ = config;

  /**
   * @type {camlistore.ServerConnection}
   * @private
   */
  this.connection_ = new camlistore.ServerConnection(config);

  /**
   * @type {camlistore.BlobItemContainer}
   * @private
   */
  this.blobItemContainer_ = new camlistore.BlobItemContainer(
      this.connection_, opt_domHelper);
  this.blobItemContainer_.setHasCreateItem(true);

  /**
   * @type {camlistore.Toolbar}
   * @private
   */
  this.toolbar_ = new camlistore.Toolbar(opt_domHelper);

  /**
   * @type {goog.events.EventHandler}
   * @private
   */
  this.eh_ = new goog.events.EventHandler(this);
};
goog.inherits(camlistore.IndexPage, goog.ui.Component);



/**
 * Creates an initial DOM representation for the component.
 */
camlistore.IndexPage.prototype.createDom = function() {
  this.decorateInternal(this.dom_.createElement('div'));
};


/**
 * Decorates an existing HTML DIV element.
 * @param {Element} element The DIV element to decorate.
 */
camlistore.IndexPage.prototype.decorateInternal = function(element) {
  camlistore.IndexPage.superClass_.decorateInternal.call(this, element);

  var el = this.getElement();
  goog.dom.classes.add(el, 'cam-index-page');

  var titleEl = this.dom_.createDom('h1', 'cam-index-page-title');
  this.dom_.setTextContent(titleEl, this.config_.ownerName + '\'s Vault');
  this.dom_.appendChild(el, titleEl);

  this.addChild(this.toolbar_, true);
  this.addChild(this.blobItemContainer_, true);
};


/** @override */
camlistore.IndexPage.prototype.disposeInternal = function() {
  camlistore.IndexPage.superClass_.disposeInternal.call(this);
  this.eh_.dispose();
};


/**
 * Called when component's element is known to be in the document.
 */
camlistore.IndexPage.prototype.enterDocument = function() {
  camlistore.IndexPage.superClass_.enterDocument.call(this);

  goog.events.dispatchEvent(
      this.blobItemContainer_,
      camlistore.BlobItemContainer.EventType.SHOW_RECENT);
};


/**
 * Called when component's element is known to have been removed from the
 * document.
 */
camlistore.IndexPage.prototype.exitDocument = function() {
  camlistore.IndexPage.superClass_.exitDocument.call(this);
  // Clear event handlers here
};
