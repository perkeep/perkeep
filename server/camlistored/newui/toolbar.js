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
   * @type {goog.ui.ToolbarButton}
   * @private
   */
  // TODO(mpl): figure out why it is acting retarded with the positioning.
  var pm = new goog.ui.PopupMenu();
  pm.addItem(new goog.ui.MenuItem('Examples:'));
  pm.addItem(new goog.ui.MenuItem("Search for 'foo' in tags: tag:foo"));
  pm.addItem(new goog.ui.MenuItem("Search for 'bar' in titles: title:bar"));
  pm.addItem(new goog.ui.MenuItem("(Fuzzy) Search for 'baz' in all attributes: baz (broken atm?)"));
  this.helpButton_ = new goog.ui.ToolbarMenuButton('Help', pm);

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
  CHECKED_ITEMS_CREATE_SET: 'Camlistore_Toolbar_Checked_Items_Create_set'
};

/**
 * @type {boolean}
 */
camlistore.Toolbar.prototype.isSearch = "false";


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
  this.addChild(this.checkedItemsCreateSetButton_, true);
  if (this.isSearch == "true") {
    this.addChild(this.homeButton_, true);
    this.addChild(this.rootsButton_, true);
    this.addChild(this.helpButton_, true);
  } else {
    this.addChild(this.goSearchButton_, true);
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

  if (this.isSearch == "true") {
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
    this.checkedItemsCreateSetButton_.setEnabled(true);
  } else {
    this.checkedItemsCreateSetButton_.setContent('');
    this.checkedItemsCreateSetButton_.setEnabled(false);
  }
};

