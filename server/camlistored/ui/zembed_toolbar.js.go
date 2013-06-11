// THIS FILE IS AUTO-GENERATED FROM toolbar.js
// DO NOT EDIT.

package newui

import "time"

import "camlistore.org/pkg/fileembed"

func init() {
	Files.Add("toolbar.js", 8099, time.Unix(0, 1370942742232957700), fileembed.String("/**\n"+
		" * @fileoverview Toolbar for manipulating the display of the blob index page.\n"+
		" *\n"+
		" */\n"+
		"goog.provide('camlistore.Toolbar');\n"+
		"goog.provide('camlistore.Toolbar.EventType');\n"+
		"\n"+
		"goog.require('goog.dom');\n"+
		"goog.require('goog.dom.classes');\n"+
		"goog.require('goog.events.EventHandler');\n"+
		"goog.require('goog.events.EventType');\n"+
		"goog.require('goog.ui.MenuItem');\n"+
		"goog.require('goog.ui.PopupMenu');\n"+
		"goog.require('goog.ui.Toolbar');\n"+
		"goog.require('goog.ui.ToolbarButton');\n"+
		"goog.require('goog.ui.ToolbarMenuButton');\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {goog.dom.DomHelper=} opt_domHelper DOM helper to use.\n"+
		" *\n"+
		" * @extends {goog.ui.Toolbar}\n"+
		" * @constructor\n"+
		" */\n"+
		"camlistore.Toolbar = function(opt_domHelper) {\n"+
		"  goog.base(this, opt_domHelper);\n"+
		"\n"+
		"  /**\n"+
		"   * @type {boolean}\n"+
		"   */\n"+
		"  this.isSearch = false;\n"+
		"\n"+
		"  /**\n"+
		"   * @type {goog.ui.ToolbarButton}\n"+
		"   * @private\n"+
		"   */\n"+
		"  this.biggerButton_ = new goog.ui.ToolbarButton('+');\n"+
		"  this.biggerButton_.addClassName('cam-toolbar-magnify');\n"+
		"\n"+
		"  /**\n"+
		"   * @type {goog.ui.ToolbarButton}\n"+
		"   * @private\n"+
		"   */\n"+
		"  this.smallerButton_ = new goog.ui.ToolbarButton('\\u2212');\n"+
		"  this.smallerButton_.addClassName('cam-toolbar-magnify');\n"+
		"\n"+
		"  /**\n"+
		"   * @type {goog.ui.ToolbarButton}\n"+
		"   * @private\n"+
		"   */\n"+
		"  this.checkedItemsCreateSetButton_ = new goog.ui.ToolbarButton('');\n"+
		"  this.checkedItemsCreateSetButton_.addClassName('cam-checked-items');\n"+
		"\n"+
		"  /**\n"+
		"   * @type {goog.ui.ToolbarButton}\n"+
		"   * @private\n"+
		"   */\n"+
		"  this.checkedItemsAddToSetButton_ = new goog.ui.ToolbarButton('Add to Set');\n"+
		"  this.checkedItemsAddToSetButton_.addClassName('cam-checked-items');\n"+
		"  this.checkedItemsAddToSetButton_.setEnabled(false);\n"+
		"\n"+
		"  /**\n"+
		"   * @type {goog.ui.ToolbarButton}\n"+
		"   * @private\n"+
		"   */\n"+
		"  this.setAsCollecButton_ = new goog.ui.ToolbarButton('Select as current Set');\n"+
		"  this.setAsCollecButton_.addClassName('cam-checked-items');\n"+
		"  this.setAsCollecButton_.setEnabled(false);\n"+
		"\n"+
		"\n"+
		"  /**\n"+
		"   * Used only on the search page\n"+
		"   * @type {goog.ui.ToolbarButton}\n"+
		"   * @private\n"+
		"   */\n"+
		"  this.rootsButton_ = new goog.ui.ToolbarButton('Search Roots');\n"+
		"  this.rootsButton_.addClassName('cam-checked-items');\n"+
		"\n"+
		"  /**\n"+
		"   * Used only on the search page\n"+
		"   * @type {goog.ui.ToolbarButton}\n"+
		"   * @private\n"+
		"   */\n"+
		"  this.homeButton_ = new goog.ui.ToolbarButton('Home');\n"+
		"  this.homeButton_.addClassName('cam-checked-items');\n"+
		"\n"+
		"  /**\n"+
		"   * Used only on the search page\n"+
		"   * @type {goog.ui.ToolbarMenuButton}\n"+
		"   * @private\n"+
		"   */\n"+
		"  // TODO(mpl): figure out why it is acting retarded with the positioning.\n"+
		"  this.helpButton_ = new goog.ui.ToolbarMenuButton('Help');\n"+
		"  this.helpButton_.addItem(new goog.ui.MenuItem('Usage examples (omit the double-"+
		"quotes):'));\n"+
		"  this.helpButton_.addItem(new goog.ui.MenuItem(\"Search for 'foo' in tags: \\\"tag:"+
		"foo\\\"\"));\n"+
		"  this.helpButton_.addItem(new goog.ui.MenuItem(\"Search for 'bar' in titles: \\\"ti"+
		"tle:bar\\\"\"));\n"+
		"  this.helpButton_.addItem(new goog.ui.MenuItem(\"Search for permanode with blobre"+
		"f XXX: \\\"bref:XXX\\\"\"));\n"+
		"  this.helpButton_.addItem(new goog.ui.MenuItem(\"(Fuzzy) Search for 'baz' in all "+
		"attributes: \\\"baz\\\" (broken atm?)\"));\n"+
		"\n"+
		"  /**\n"+
		"   * Used only on the index page\n"+
		"   * @type {goog.ui.ToolbarButton}\n"+
		"   * @private\n"+
		"   */\n"+
		"  this.goSearchButton_ = new goog.ui.ToolbarButton('Search');\n"+
		"  this.goSearchButton_.addClassName('cam-checked-items');\n"+
		"\n"+
		"  /**\n"+
		"   * @type {goog.events.EventHandler}\n"+
		"   * @private\n"+
		"   */\n"+
		"  this.eh_ = new goog.events.EventHandler(this);\n"+
		"};\n"+
		"goog.inherits(camlistore.Toolbar, goog.ui.Toolbar);\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @enum {string}\n"+
		" */\n"+
		"camlistore.Toolbar.EventType = {\n"+
		"  BIGGER: 'Camlistore_Toolbar_Bigger',\n"+
		"  SMALLER: 'Camlistore_Toolbar_Smaller',\n"+
		"  HOME: 'Camlistore_Toolbar_Home',\n"+
		"  ROOTS: 'Camlistore_Toolbar_SearchRoots',\n"+
		"  GOSEARCH: 'Camlistore_Toolbar_GoSearch',\n"+
		"  HELP: 'Camlistore_Toolbar_Help',\n"+
		"  CHECKED_ITEMS_ADDTO_SET: 'Camlistore_Toolbar_Checked_Items_Addto_set',\n"+
		"  SELECT_COLLEC: 'Camlistore_Toolbar_Select_collec',\n"+
		"  CHECKED_ITEMS_CREATE_SET: 'Camlistore_Toolbar_Checked_Items_Create_set'\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * Creates an initial DOM representation for the component.\n"+
		" */\n"+
		"camlistore.Toolbar.prototype.createDom = function() {\n"+
		"  this.decorateInternal(this.dom_.createElement('div'));\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * Decorates an existing HTML DIV element.\n"+
		" * @param {Element} el The DIV element to decorate.\n"+
		" */\n"+
		"camlistore.Toolbar.prototype.decorateInternal = function(el) {\n"+
		"  camlistore.Toolbar.superClass_.decorateInternal.call(this, el);\n"+
		"\n"+
		"  this.addChild(this.biggerButton_, true);\n"+
		"  this.addChild(this.smallerButton_, true);\n"+
		"  this.addChild(this.checkedItemsCreateSetButton_, true);\n"+
		"  this.addChild(this.setAsCollecButton_, true);\n"+
		"  this.addChild(this.checkedItemsAddToSetButton_, true);\n"+
		"  if (this.isSearch == true) {\n"+
		"    this.addChild(this.rootsButton_, true);\n"+
		"    this.addChild(this.homeButton_, true);\n"+
		"    this.addChild(this.helpButton_, true);\n"+
		"  } else {\n"+
		"    this.addChild(this.goSearchButton_, true);\n"+
		"  }\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/** @override */\n"+
		"camlistore.Toolbar.prototype.disposeInternal = function() {\n"+
		"  camlistore.Toolbar.superClass_.disposeInternal.call(this);\n"+
		"  this.eh_.dispose();\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * Called when component's element is known to be in the document.\n"+
		" */\n"+
		"camlistore.Toolbar.prototype.enterDocument = function() {\n"+
		"  camlistore.Toolbar.superClass_.enterDocument.call(this);\n"+
		"\n"+
		"  this.eh_.listen(\n"+
		"      this.biggerButton_.getElement(),\n"+
		"      goog.events.EventType.CLICK,\n"+
		"      goog.bind(this.dispatch_, this, camlistore.Toolbar.EventType.BIGGER));\n"+
		"\n"+
		"  this.eh_.listen(\n"+
		"      this.smallerButton_.getElement(),\n"+
		"      goog.events.EventType.CLICK,\n"+
		"      goog.bind(this.dispatch_, this, camlistore.Toolbar.EventType.SMALLER));\n"+
		"\n"+
		"  if (this.isSearch == true) {\n"+
		"\n"+
		"    this.eh_.listen(\n"+
		"      this.rootsButton_.getElement(),\n"+
		"      goog.events.EventType.CLICK,\n"+
		"      goog.bind(this.dispatch_, this, camlistore.Toolbar.EventType.ROOTS));\n"+
		"\n"+
		"    this.eh_.listen(\n"+
		"      this.homeButton_.getElement(),\n"+
		"      goog.events.EventType.CLICK,\n"+
		"      goog.bind(this.dispatch_, this, camlistore.Toolbar.EventType.HOME));\n"+
		"\n"+
		"  } else {\n"+
		"\n"+
		"    this.eh_.listen(\n"+
		"      this.goSearchButton_.getElement(),\n"+
		"      goog.events.EventType.CLICK,\n"+
		"      goog.bind(this.dispatch_, this, camlistore.Toolbar.EventType.GOSEARCH));\n"+
		"\n"+
		"  }\n"+
		"\n"+
		"  this.eh_.listen(\n"+
		"      this.checkedItemsCreateSetButton_.getElement(),\n"+
		"      goog.events.EventType.CLICK,\n"+
		"      goog.bind(this.dispatch_, this,\n"+
		"                camlistore.Toolbar.EventType.CHECKED_ITEMS_CREATE_SET));\n"+
		"\n"+
		"  this.eh_.listen(\n"+
		"      this.setAsCollecButton_.getElement(),\n"+
		"      goog.events.EventType.CLICK,\n"+
		"      goog.bind(this.dispatch_, this,\n"+
		"                camlistore.Toolbar.EventType.SELECT_COLLEC));\n"+
		"\n"+
		"  this.eh_.listen(\n"+
		"      this.checkedItemsAddToSetButton_.getElement(),\n"+
		"      goog.events.EventType.CLICK,\n"+
		"      goog.bind(this.dispatch_, this,\n"+
		"                camlistore.Toolbar.EventType.CHECKED_ITEMS_ADDTO_SET));\n"+
		"\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * @param {camlistore.Toolbar.EventType} eventType Type of event to dispatch.\n"+
		" */\n"+
		"camlistore.Toolbar.prototype.dispatch_ = function(eventType) {\n"+
		"  this.dispatchEvent(eventType);\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * Called when component's element is known to have been removed from the\n"+
		" * document.\n"+
		" */\n"+
		"camlistore.Toolbar.prototype.exitDocument = function() {\n"+
		"  camlistore.Toolbar.superClass_.exitDocument.call(this);\n"+
		"  // Clear event handlers here\n"+
		"};\n"+
		"\n"+
		"\n"+
		"/**\n"+
		" * TODO: i18n.\n"+
		" * @param {number} count Number of items.\n"+
		" */\n"+
		"camlistore.Toolbar.prototype.setCheckedBlobItemCount = function(count) {\n"+
		"  if (count) {\n"+
		"    var txt = 'Create set w/ ' + count + ' item' + (count > 1 ? 's' : '');\n"+
		"    this.checkedItemsCreateSetButton_.setContent(txt);\n"+
		"    this.checkedItemsCreateSetButton_.setEnabled(true);\n"+
		"  } else {\n"+
		"    this.checkedItemsCreateSetButton_.setContent('');\n"+
		"    this.checkedItemsCreateSetButton_.setEnabled(false);\n"+
		"  }\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * TODO: i18n.\n"+
		" * @param {boolean} enable\n"+
		" */\n"+
		"camlistore.Toolbar.prototype.toggleCollecButton = function(enable) {\n"+
		"  if (enable) {\n"+
		"    this.setAsCollecButton_.setEnabled(true);\n"+
		"  } else {\n"+
		"    this.setAsCollecButton_.setEnabled(false);\n"+
		"  }\n"+
		"};\n"+
		"\n"+
		"/**\n"+
		" * TODO: i18n.\n"+
		" * @param {boolean} enable\n"+
		" */\n"+
		"camlistore.Toolbar.prototype.toggleAddToSetButton = function(enable) {\n"+
		"  if (enable) {\n"+
		"    this.checkedItemsAddToSetButton_.setEnabled(true);\n"+
		"  } else {\n"+
		"    this.checkedItemsAddToSetButton_.setEnabled(false);\n"+
		"  }\n"+
		"};\n"+
		""))
}
