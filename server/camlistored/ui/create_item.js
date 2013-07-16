/**
 * @fileoverview Placeholder BlobItem in a BlobItemContainer that lets the
 * user upload new blobs to the server.
 *
 */
goog.provide('camlistore.CreateItem');

goog.require('goog.dom');
goog.require('goog.dom.classes');
goog.require('goog.events.EventHandler');
goog.require('goog.events.EventType');
goog.require('goog.ui.Control');


/**
 * @param {goog.dom.DomHelper=} opt_domHelper DOM helper to use.
 *
 * @extends {goog.ui.Control}
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
  goog.dom.classes.add(el, 'cam-blobitem', 'cam-createitem');

  var plusEl = this.dom_.createDom('a', 'cam-createitem-link');
  plusEl.href = 'javascript:void(0)';
  this.dom_.setTextContent(plusEl, '+')
  this.dom_.appendChild(el, plusEl);

  var titleEl = this.dom_.createDom('p', 'cam-createitem-thumbtitle');
  this.dom_.setTextContent(titleEl, 'Drag & drop files or click');
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
	var plusEl = goog.dom.getFirstElementChild(this.getElement());
	this.eh_.listen(
		plusEl,
		goog.events.EventType.DRAGENTER,
		this.handleFileDragEnter_);
	this.eh_.listen(
		plusEl,
		goog.events.EventType.DRAGLEAVE,
		this.handleFileDragLeave_);
};

/**
 * @param {goog.events.Event} e The drag drop event.
 * @private
 */
camlistore.CreateItem.prototype.handleFileDragEnter_ = function(e) {
	e.preventDefault();
	e.stopPropagation();
	goog.dom.classes.add(this.getElement(), 'cam-blobitem-dropactive');
	var container = this.getParent();
	container.notifyDragEnter_(this);
};

/**
 * @param {goog.events.Event} e The drag drop event.
 * @private
 */
camlistore.CreateItem.prototype.handleFileDragLeave_ = function(e) {
	e.preventDefault();
	e.stopPropagation();
	goog.dom.classes.remove(this.getElement(), 'cam-blobitem-dropactive');
	var container = this.getParent();
	container.notifyDragLeave_(this);
};

/**
 * Called when component's element is known to have been removed from the
 * document.
 */
camlistore.CreateItem.prototype.exitDocument = function() {
  camlistore.CreateItem.superClass_.exitDocument.call(this);
  this.eh_.removeAll();
};
