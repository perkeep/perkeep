/**
 * @fileoverview Entry point for the blob browser UI.
 *
 */
goog.provide('camlistore.IndexPage');

goog.require('goog.array');
goog.require('goog.dom');
goog.require('goog.dom.classes');
goog.require('goog.events.EventHandler');
goog.require('goog.events.EventType');
goog.require('goog.ui.Component');
goog.require('goog.ui.Textarea');
goog.require('camlistore.BlobItemContainer');
goog.require('camlistore.ServerConnection');
goog.require('camlistore.Toolbar');
goog.require('camlistore.Toolbar.EventType');
goog.require('camlistore.ServerType');


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
   * @type {Element}
   * @private
   */
  this.serverInfo_;

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

  var titleEl = this.dom_.createDom('h1', 'cam-index-title');
  this.dom_.setTextContent(titleEl, this.config_.ownerName + '\'s Vault');
  this.dom_.appendChild(el, titleEl);

  this.serverInfo_ = this.dom_.createDom('div', 'cam-index-serverinfo');
  this.dom_.appendChild(el, this.serverInfo_);

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

	this.connection_.serverStatus(
		goog.bind(function(resp) {
			this.handleServerStatus_(resp);
		}, this)
	);

  this.eh_.listen(
      this.toolbar_, camlistore.Toolbar.EventType.BIGGER,
      function() {
        if (this.blobItemContainer_.bigger()) {
          this.blobItemContainer_.showRecent();
        }
      });

  this.eh_.listen(
      this.toolbar_, camlistore.Toolbar.EventType.SMALLER,
      function() {
        if (this.blobItemContainer_.smaller()) {
          this.blobItemContainer_.showRecent();
        }
      });

  this.eh_.listen(
      this.toolbar_, camlistore.Toolbar.EventType.GOSEARCH,
      function() {
        window.open('./search.html', 'Search');
      });

  this.eh_.listen(
      this.toolbar_, camlistore.Toolbar.EventType.CHECKED_ITEMS_CREATE_SET,
      function() {
        var blobItems = this.blobItemContainer_.getCheckedBlobItems();
        this.createNewSetWithItems_(blobItems);
      });

  this.eh_.listen(
      this.toolbar_, camlistore.Toolbar.EventType.CHECKED_ITEMS_ADDTO_SET,
      function() {
        var blobItems = this.blobItemContainer_.getCheckedBlobItems();
        this.addItemsToSet_(blobItems);
      });

  this.eh_.listen(
      this.toolbar_, camlistore.Toolbar.EventType.SELECT_COLLEC,
      function() {
        var blobItems = this.blobItemContainer_.getCheckedBlobItems();
        // there should be only one item selected
        if (blobItems.length != 1) {
          alert("Cannet set multiple items as current collection");
          return;
        }
        this.blobItemContainer_.currentCollec_ = blobItems[0].blobRef_;
        this.blobItemContainer_.unselectAll();
        this.toolbar_.setCheckedBlobItemCount(0);
        this.toolbar_.toggleCollecButton(false);
        this.toolbar_.toggleAddToSetButton(false);
      });

  // TODO(mpl): those are getting large. make dedicated funcs.
  this.eh_.listen(
      this.blobItemContainer_,
      camlistore.BlobItemContainer.EventType.BLOB_ITEMS_CHOSEN,
      function() {
        var blobItems = this.blobItemContainer_.getCheckedBlobItems();
        this.toolbar_.setCheckedBlobItemCount(blobItems.length);
        // set checkedItemsAddToSetButton_
        if (this.blobItemContainer_.currentCollec_ &&
          this.blobItemContainer_.currentCollec_ != "" &&
          blobItems.length > 0) {
          this.toolbar_.toggleAddToSetButton(true);
        } else {
          this.toolbar_.toggleAddToSetButton(false);
        }
        // set setAsCollecButton_
        this.toolbar_.toggleCollecButton(false);
      });

  this.eh_.listen(
      this.blobItemContainer_,
      camlistore.BlobItemContainer.EventType.SINGLE_NODE_CHOSEN,
      function() {
        var blobItems = this.blobItemContainer_.getCheckedBlobItems();
        this.toolbar_.setCheckedBlobItemCount(blobItems.length);
        // set checkedItemsAddToSetButton_
        if (this.blobItemContainer_.currentCollec_ &&
          this.blobItemContainer_.currentCollec_ != "" &&
          blobItems.length > 0) {
          this.toolbar_.toggleAddToSetButton(true);
        } else {
          this.toolbar_.toggleAddToSetButton(false);
        }
        // set setAsCollecButton_
        if (blobItems.length == 1 &&
          blobItems[0].isCollection()) {
          this.toolbar_.toggleCollecButton(true);
        } else {
          this.toolbar_.toggleCollecButton(false);
        }
      });

  this.blobItemContainer_.showRecent();
};


/**
 * Called when component's element is known to have been removed from the
 * document.
 */
camlistore.IndexPage.prototype.exitDocument = function() {
  camlistore.IndexPage.superClass_.exitDocument.call(this);
  // Clear event handlers here
};


/**
 * @param {Array.<camlistore.BlobItem>} blobItems Items to add to the permanode.
 * @private
 */
camlistore.IndexPage.prototype.createNewSetWithItems_ = function(blobItems) {
  this.connection_.createPermanode(
      goog.bind(this.addMembers_, this, true, blobItems));
};

/**
 * @param {Array.<camlistore.BlobItem>} blobItems Items to add to the permanode.
 * @private
 */
camlistore.IndexPage.prototype.addItemsToSet_ = function(blobItems) {
	if (!this.blobItemContainer_.currentCollec_ ||
		this.blobItemContainer_.currentCollec_ == "") {
		alert("no destination collection selected");
	}
	this.addMembers_(false, blobItems, this.blobItemContainer_.currentCollec_);
};

/**
 * @param {boolean} newSet Whether the containing set has just been created.
 * @param {Array.<camlistore.BlobItem>} blobItems Items to add to the permanode.
 * @param {string} permanode Node to add the items to.
 * @private
 */
camlistore.IndexPage.prototype.addMembers_ =
    function(newSet, blobItems, permanode) {
  var deferredList = [];
  var complete = goog.bind(this.addItemsToSetDone_, this, permanode);
  var callback = function() {
    deferredList.push(1);
    if (deferredList.length == blobItems.length) {
      complete();
    }
  };

  // TODO(mpl): newSet is a lame trick. Do better.
  if (newSet) {
    this.connection_.newSetAttributeClaim(
      permanode, 'title', 'My new set', function() {}
    );
  }
  goog.array.forEach(blobItems, function(blobItem, index) {
    this.connection_.newAddAttributeClaim(
        permanode, 'camliMember', blobItem.getBlobRef(), callback);
  }, this);
};


/**
 * @param {string} permanode Node to which the items were added.
 * @private
 */
camlistore.IndexPage.prototype.addItemsToSetDone_ = function(permanode) {
  this.blobItemContainer_.unselectAll();
  this.toolbar_.setCheckedBlobItemCount(0);
  this.toolbar_.toggleCollecButton(false);
  this.toolbar_.toggleAddToSetButton(false);
  this.blobItemContainer_.showRecent();
};

/**
 * @param {camlistore.ServerType.StatusResponse} resp response for a status request
 * @private
 */
camlistore.IndexPage.prototype.handleServerStatus_ =
function(resp) {
	if (resp == null) {
		return;
	}
	goog.dom.removeChildren(this.serverInfo_);
	if (resp.version) {
		var version = "Camlistore version: " + resp.version + "\n";
		var div = this.dom_.createDom('div');
		goog.dom.setTextContent(div, version);
		goog.dom.appendChild(this.serverInfo_, div);
	}
};

