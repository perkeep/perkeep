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
goog.require('goog.ui.MenuItem');
goog.require('goog.ui.PopupMenu');
goog.require('goog.ui.Toolbar');
goog.require('goog.ui.ToolbarButton');
goog.require('goog.ui.ToolbarMenuButton');


/**
 * @param {goog.dom.DomHelper=} opt_domHelper DOM helper to use.
 *
 * @extends {goog.ui.Toolbar}
 * @constructor
 */
camlistore.Toolbar = function(opt_domHelper) {
  goog.base(this, opt_domHelper);

  /**
   * @type {HTMLFormElement}
   * @private
   */
  this.searchform_ = this.dom_.createDom('form', 'cam-searchform');

  /**
   * @type {goog.ui.Button}
   * @private
   */
  this.biggerButton_ = new goog.ui.Button('+');
  this.biggerButton_.addClassName('cam-bigger');

  /**
   * @type {goog.ui.Button}
   * @private
   */
  this.smallerButton_ = new goog.ui.Button('\u2212');
  this.smallerButton_.addClassName('cam-smaller');

  /**
   * @type {goog.ui.Button}
   * @private
   */
  this.checkedItemsCreateSetButton_ = new goog.ui.Button('');
  this.checkedItemsCreateSetButton_.addClassName('cam-checked-items');
  this.checkedItemsCreateSetButton_.setVisible(false);

  /**
   * @type {goog.ui.Button}
   * @private
   * /
  this.clearSelectionButton_ = new goog.ui.Button('Clear Selection');
  this.clearSelectionButton_.setVisible(false);

  /**
   * @type {goog.ui.Button}
   * @private
   */
  this.createPermanodeButton_ = new goog.ui.Button('New Permanode');
  this.createPermanodeButton_.addClassName('cam-toolbar-createpermanode');

  /**
   * @type {goog.ui.Button}
   * @private
   */
  this.checkedItemsAddToSetButton_ = new goog.ui.Button('Add to Set');
  this.checkedItemsAddToSetButton_.addClassName('cam-checked-items');
  this.checkedItemsAddToSetButton_.setEnabled(false);

  /**
   * @type {goog.ui.Button}
   * @private
   */
  this.setAsCollecButton_ = new goog.ui.Button('Select as current Set');
  this.setAsCollecButton_.addClassName('cam-checked-items');
  this.setAsCollecButton_.setEnabled(false);


  /**
   * Used only on the search page
   * @type {goog.ui.Button}
   * @private
   */
  this.rootsButton_ = new goog.ui.Button('Search Roots');
  this.rootsButton_.addClassName('cam-checked-items');

  /**
   * Used to display random statusy stuff.
   */
  this.status_ = null;

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
  SEARCH: 'Camlistore_Toolbar_Search',
  BIGGER: 'Camlistore_Toolbar_Bigger',
  SMALLER: 'Camlistore_Toolbar_Smaller',
  ROOTS: 'Camlistore_Toolbar_SearchRoots',
  CHECKED_ITEMS_ADDTO_SET: 'Camlistore_Toolbar_Checked_Items_Addto_set',
  CLEAR_SELECTION: 'Clear_Selection',
  SELECT_COLLEC: 'Camlistore_Toolbar_Select_collec',
  CHECKED_ITEMS_CREATE_SET: 'Camlistore_Toolbar_Checked_Items_Create_set',
  CREATE_PERMANODE: 'Camlistore_Toolbar_Create_Permanode',
};


/**
 * @return {goog.ui.Control}
 */
camlistore.Toolbar.prototype.setStatus = function(text) {
  goog.dom.setTextContent(this.status_, text);
};


camlistore.Toolbar.prototype.getSearchText = function() {
  return this.searchbox_.value;
};


/**
 * Creates an initial DOM representation for the component.
 */
camlistore.Toolbar.prototype.createDom = function() {
  this.decorateInternal(this.dom_.createElement('div'));
};


/**
 * Decorates an existing HTML DIV element.
 * @param {Element} el The DIV element to decorate.
 */
camlistore.Toolbar.prototype.decorateInternal = function(el) {
  camlistore.Toolbar.superClass_.decorateInternal.call(this, el);
  this.getElement().className = 'cam-toolbar';

  this.searchbox_ = this.dom_.createDom('input', 'cam-searchbox');
  this.searchbox_.setAttribute('placeholder', 'Search...');
  this.searchbox_.title = '"title:monkey", "tag:cheese", etc...';
  this.searchform_.appendChild(this.searchbox_);
  this.getElement().appendChild(this.searchform_);

  this.addChild(this.smallerButton_, true);
  this.addChild(this.biggerButton_, true);
  this.addChild(this.checkedItemsCreateSetButton_, true);
  this.addChild(this.clearSelectionButton_, true);
  this.addChild(this.createPermanodeButton_, true);
  this.addChild(this.setAsCollecButton_, true);
  this.addChild(this.checkedItemsAddToSetButton_, true);
  this.addChild(this.rootsButton_, true);

  this.status_ = this.dom_.createDom('div', 'cam-toolbar-status');
  this.getElement().appendChild(this.status_);
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
  goog.style.setUnselectable(this.searchbox_, false);

  this.eh_.listen(
      this.searchform_,
      goog.events.EventType.SUBMIT,
      function(e) {
        e.stopPropagation();
        e.preventDefault();
        this.dispatch_(camlistore.Toolbar.EventType.SEARCH);
      }.bind(this));

  this.eh_.listen(
      this.biggerButton_.getElement(),
      goog.events.EventType.CLICK,
      goog.bind(this.dispatch_, this, camlistore.Toolbar.EventType.BIGGER));

  this.eh_.listen(
      this.smallerButton_.getElement(),
      goog.events.EventType.CLICK,
      goog.bind(this.dispatch_, this, camlistore.Toolbar.EventType.SMALLER));

  this.eh_.listen(
      this.rootsButton_.getElement(),
      goog.events.EventType.CLICK,
      goog.bind(this.dispatch_, this, camlistore.Toolbar.EventType.ROOTS));

  this.eh_.listen(
      this.checkedItemsCreateSetButton_.getElement(),
      goog.events.EventType.CLICK,
      goog.bind(this.dispatch_, this,
                camlistore.Toolbar.EventType.CHECKED_ITEMS_CREATE_SET));

  this.eh_.listen(
      this.clearSelectionButton_.getElement(),
      goog.events.EventType.CLICK,
      goog.bind(this.dispatch_, this,
                camlistore.Toolbar.EventType.CLEAR_SELECTION));

  this.eh_.listen(
      this.createPermanodeButton_.getElement(),
      goog.events.EventType.CLICK,
      goog.bind(this.dispatch_, this,
                camlistore.Toolbar.EventType.CREATE_PERMANODE));

  this.eh_.listen(
      this.setAsCollecButton_.getElement(),
      goog.events.EventType.CLICK,
      goog.bind(this.dispatch_, this,
                camlistore.Toolbar.EventType.SELECT_COLLEC));

  this.eh_.listen(
      this.checkedItemsAddToSetButton_.getElement(),
      goog.events.EventType.CLICK,
      goog.bind(this.dispatch_, this,
                camlistore.Toolbar.EventType.CHECKED_ITEMS_ADDTO_SET));

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


/**
 * TODO: i18n.
 * @param {number} count Number of items.
 */
camlistore.Toolbar.prototype.setCheckedBlobItemCount = function(count) {
  if (count) {
    var txt = 'Create set w/ ' + count + ' item' + (count > 1 ? 's' : '');
    this.checkedItemsCreateSetButton_.setContent(txt);
    this.checkedItemsCreateSetButton_.setVisible(true);
    this.clearSelectionButton_.setVisible(true);
  } else {
    this.checkedItemsCreateSetButton_.setContent('');
    this.checkedItemsCreateSetButton_.setVisible(false);
    this.clearSelectionButton_.setVisible(false);
  }
};

/**
 * TODO: i18n.
 * @param {boolean} enable
 */
camlistore.Toolbar.prototype.toggleCollecButton = function(enable) {
  if (enable) {
    this.setAsCollecButton_.setEnabled(true);
  } else {
    this.setAsCollecButton_.setEnabled(false);
  }
};

/**
 * TODO: i18n.
 * @param {boolean} enable
 */
camlistore.Toolbar.prototype.toggleAddToSetButton = function(enable) {
  if (enable) {
    this.checkedItemsAddToSetButton_.setEnabled(true);
  } else {
    this.checkedItemsAddToSetButton_.setEnabled(false);
  }
};
