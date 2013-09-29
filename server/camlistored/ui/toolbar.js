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
   * @type {boolean}
   */
  this.isSearch = false;

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
   * @type {goog.ui.ToolbarButton}
   * @private
   */
  this.checkedItemsCreateSetButton_ = new goog.ui.ToolbarButton('');
  this.checkedItemsCreateSetButton_.addClassName('cam-checked-items');
  this.checkedItemsCreateSetButton_.setVisible(false);

  /**
   * @type {goog.ui.ToolbarButton}
   * @private
   */
  this.createPermanodeButton_ = new goog.ui.ToolbarButton('New Permanode');
  this.createPermanodeButton_.addClassName('cam-toolbar-createpermanode');

  /**
   * @type {goog.ui.ToolbarButton}
   * @private
   */
  this.checkedItemsAddToSetButton_ = new goog.ui.ToolbarButton('Add to Set');
  this.checkedItemsAddToSetButton_.addClassName('cam-checked-items');
  this.checkedItemsAddToSetButton_.setEnabled(false);

  /**
   * @type {goog.ui.ToolbarButton}
   * @private
   */
  this.setAsCollecButton_ = new goog.ui.ToolbarButton('Select as current Set');
  this.setAsCollecButton_.addClassName('cam-checked-items');
  this.setAsCollecButton_.setEnabled(false);


  /**
   * Used only on the search page
   * @type {goog.ui.ToolbarButton}
   * @private
   */
  this.rootsButton_ = new goog.ui.ToolbarButton('Search Roots');
  this.rootsButton_.addClassName('cam-checked-items');

  /**
   * Used only on the search page
   * @type {goog.ui.ToolbarButton}
   * @private
   */
  this.homeButton_ = new goog.ui.ToolbarButton('Home');
  this.homeButton_.addClassName('cam-checked-items');

  /**
   * Used only on the search page
   * @type {goog.ui.ToolbarMenuButton}
   * @private
   */
  // TODO(mpl): figure out why it is acting retarded with the positioning.
  this.helpButton_ = new goog.ui.ToolbarMenuButton('Help');
  this.helpButton_.addItem(new goog.ui.MenuItem('Usage examples (omit the double-quotes):'));
  this.helpButton_.addItem(new goog.ui.MenuItem("Search for 'foo' in tags: \"tag:foo\""));
  this.helpButton_.addItem(new goog.ui.MenuItem("Search for 'bar' in titles: \"title:bar\""));
  this.helpButton_.addItem(new goog.ui.MenuItem("Search for permanode with blobref XXX: \"bref:XXX\""));
  this.helpButton_.addItem(new goog.ui.MenuItem("(Fuzzy) Search for 'baz' in all attributes: \"baz\" (broken atm?)"));

  /**
   * Used only on the index page
   * @type {goog.ui.ToolbarButton}
   * @private
   */
  this.goSearchButton_ = new goog.ui.ToolbarButton('Search');
  this.goSearchButton_.addClassName('cam-checked-items');

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
  SMALLER: 'Camlistore_Toolbar_Smaller',
  HOME: 'Camlistore_Toolbar_Home',
  ROOTS: 'Camlistore_Toolbar_SearchRoots',
  GOSEARCH: 'Camlistore_Toolbar_GoSearch',
  HELP: 'Camlistore_Toolbar_Help',
  CHECKED_ITEMS_ADDTO_SET: 'Camlistore_Toolbar_Checked_Items_Addto_set',
  SELECT_COLLEC: 'Camlistore_Toolbar_Select_collec',
  CHECKED_ITEMS_CREATE_SET: 'Camlistore_Toolbar_Checked_Items_Create_set',
  CREATE_PERMANODE: 'Camlistore_Toolbar_Create_Permanode',
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
  goog.dom.classes.add(this.getElement(), 'cam-toolbar');
  this.addChild(this.biggerButton_, true);
  this.addChild(this.smallerButton_, true);
  this.addChild(this.checkedItemsCreateSetButton_, true);
  this.addChild(this.createPermanodeButton_, true);
  this.addChild(this.setAsCollecButton_, true);
  this.addChild(this.checkedItemsAddToSetButton_, true);
  this.addChild(this.rootsButton_, true);
  this.addChild(this.homeButton_, true);
  this.addChild(this.helpButton_, true);
  this.addChild(this.goSearchButton_, true);

  if (this.isSearch) {
    this.goSearchButton_.setVisible(false);
    this.createPermanodeButton_.setVisible(false);
  } else {
    this.rootsButton_.setVisible(false);
    this.homeButton_.setVisible(false);
    this.helpButton_.setVisible(false);
  }
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

  if (this.isSearch == true) {

    this.eh_.listen(
      this.rootsButton_.getElement(),
      goog.events.EventType.CLICK,
      goog.bind(this.dispatch_, this, camlistore.Toolbar.EventType.ROOTS));

    this.eh_.listen(
      this.homeButton_.getElement(),
      goog.events.EventType.CLICK,
      goog.bind(this.dispatch_, this, camlistore.Toolbar.EventType.HOME));

  } else {

    this.eh_.listen(
      this.goSearchButton_.getElement(),
      goog.events.EventType.CLICK,
      goog.bind(this.dispatch_, this, camlistore.Toolbar.EventType.GOSEARCH));

  }

  this.eh_.listen(
      this.checkedItemsCreateSetButton_.getElement(),
      goog.events.EventType.CLICK,
      goog.bind(this.dispatch_, this,
                camlistore.Toolbar.EventType.CHECKED_ITEMS_CREATE_SET));

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
  } else {
    this.checkedItemsCreateSetButton_.setContent('');
    this.checkedItemsCreateSetButton_.setVisible(false);
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
