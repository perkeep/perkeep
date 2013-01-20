/**
 * @fileoverview Toolbar for manipulating the display of the blob index page.
 *
 */
goog.provide('camlistore.Toolbar');
goog.provide('camlistore.Toolbar.EventType');

goog.require('goog.dom');
goog.require('goog.dom.classes');
goog.require('goog.events.EventHandler');
goog.require('goog.events.EventType');
goog.require('goog.ui.Toolbar');
goog.require('goog.ui.ToolbarButton');


/**
 * @param {goog.dom.DomHelper=} opt_domHelper DOM helper to use.
 *
 * @extends {goog.ui.Toolbar}
 * @constructor
 */
camlistore.Toolbar = function(opt_domHelper) {
  goog.base(this, opt_domHelper);

  /**
   * @type {goog.ui.ToolbarButton}
   * @private
   */
  this.biggerButton_ = new goog.ui.ToolbarButton('+');
  this.biggerButton_.addClassName('cam-toolbar-magnify');

  /**
   * @type {goog.ui.ToolbarButton}
   * @private
   */
  this.smallerButton_ = new goog.ui.ToolbarButton('\u2212');
  this.smallerButton_.addClassName('cam-toolbar-magnify');

  /**
   * @type {goog.events.EventHandler}
   * @private
   */
  this.eh_ = new goog.events.EventHandler(this);
};
goog.inherits(camlistore.Toolbar, goog.ui.Toolbar);


/**
 * @enum {string}
 */
camlistore.Toolbar.EventType = {
  BIGGER: 'Camlistore_Toolbar_Bigger',
  SMALLER: 'Camlistore_Toolbar_Smaller'
};




/**
 * Creates an initial DOM representation for the component.
 */
camlistore.Toolbar.prototype.createDom = function() {
  this.decorateInternal(this.dom_.createElement('div'));
};


/**
 * Decorates an existing HTML DIV element.
 * @param {Element} element The DIV element to decorate.
 */
camlistore.Toolbar.prototype.decorateInternal = function(el) {
  camlistore.Toolbar.superClass_.decorateInternal.call(this, el);

  this.addChild(this.biggerButton_, true);
  this.addChild(this.smallerButton_, true);
};


/** @override */
camlistore.Toolbar.prototype.disposeInternal = function() {
  camlistore.Toolbar.superClass_.disposeInternal.call(this);
  this.eh_.dispose();
};


/**
 * Called when component's element is known to be in the document.
 */
camlistore.Toolbar.prototype.enterDocument = function() {
  camlistore.Toolbar.superClass_.enterDocument.call(this);

  this.eh_.listen(
      this.biggerButton_.getElement(),
      goog.events.EventType.CLICK,
      goog.bind(this.dispatch_, this, camlistore.Toolbar.EventType.BIGGER));

  this.eh_.listen(
      this.smallerButton_.getElement(),
      goog.events.EventType.CLICK,
      goog.bind(this.dispatch_, this, camlistore.Toolbar.EventType.SMALLER));
};


/**
 * @param {camlistore.Toolbar.EventType} eventType Type of event to dispatch.
 */
camlistore.Toolbar.prototype.dispatch_ = function(eventType) {
  this.dispatchEvent(eventType);
};


/**
 * Called when component's element is known to have been removed from the
 * document.
 */
camlistore.Toolbar.prototype.exitDocument = function() {
  camlistore.Toolbar.superClass_.exitDocument.call(this);
  // Clear event handlers here
};
