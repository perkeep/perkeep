/**
 * @fileoverview Entry point for the permanodes search UI.
 *
 */
goog.provide('camlistore.SearchPage');

goog.require('goog.array');
goog.require('goog.dom');
goog.require('goog.dom.classes');
goog.require('goog.events.EventHandler');
goog.require('goog.events.EventType');
goog.require('goog.ui.Component');
goog.require('camlistore.BlobItemContainer');
goog.require('camlistore.ServerConnection');
goog.require('camlistore.Toolbar');
goog.require('camlistore.Toolbar.EventType');


// TODO(mpl): better help. tooltip maybe?

// TODO(mpl): make a mother class that both index.js and search.js could
// inherit from?
/**
 * @param {camlistore.ServerType.DiscoveryDocument} config Global config
 *	 of the current server this page is being rendered for.
 * @param {goog.dom.DomHelper=} opt_domHelper DOM helper to use.
 *
 * @extends {goog.ui.Component}
 * @constructor
 */
camlistore.SearchPage = function(config, opt_domHelper) {
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

	/**
	 * @type {camlistore.Toolbar}
	 * @private
	 */
	this.toolbar_ = new camlistore.Toolbar(opt_domHelper);
	this.toolbar_.isSearch = true;

	/**
	 * @type {goog.events.EventHandler}
	 * @private
	 */
	this.eh_ = new goog.events.EventHandler(this);
};
goog.inherits(camlistore.SearchPage, goog.ui.Component);


/**
 * @enum {string}
 * @private
 */
camlistore.SearchPage.prototype.searchPrefix_ = {
  TAG: 'tag:',
  TITLE: 'title:',
  BLOBREF: 'bref:'
};


/**
 * @type {number}
 * @private
 */
camlistore.SearchPage.prototype.maxInResponse_ = 100;


/**
 * Creates an initial DOM representation for the component.
 */
camlistore.SearchPage.prototype.createDom = function() {
	this.decorateInternal(this.dom_.createElement('div'));
};


/**
 * Decorates an existing HTML DIV element.
 * @param {Element} element The DIV element to decorate.
 */
camlistore.SearchPage.prototype.decorateInternal = function(element) {
	camlistore.SearchPage.superClass_.decorateInternal.call(this, element);

	var el = this.getElement();
	goog.dom.classes.add(el, 'cam-index-page');

	var titleEl = this.dom_.createDom('h1', 'cam-index-page-title');
	this.dom_.setTextContent(titleEl, "Search");
	this.dom_.appendChild(el, titleEl);

	this.addChild(this.toolbar_, true);

	var searchForm = this.dom_.createDom('form', {'id': 'searchForm'});
	var searchText = this.dom_.createDom('input',
		{'type': 'text', 'id': 'searchText', 'size': 50, 'title': 'Search'}
	);
	var btnSearch = this.dom_.createDom('input',
		{'type': 'submit', 'id': 'btnSearch', 'value': 'Search'}
	);
	goog.dom.appendChild(searchForm, searchText);
	goog.dom.appendChild(searchForm, btnSearch);
	goog.dom.appendChild(el, searchForm);
	
	this.addChild(this.blobItemContainer_, true);
};


/** @override */
camlistore.SearchPage.prototype.disposeInternal = function() {
	camlistore.SearchPage.superClass_.disposeInternal.call(this);
	this.eh_.dispose();
};


/**
 * Called when component's element is known to be in the document.
 */
camlistore.SearchPage.prototype.enterDocument = function() {
	camlistore.SearchPage.superClass_.enterDocument.call(this);

	this.eh_.listen(
		this.toolbar_, camlistore.Toolbar.EventType.BIGGER,
		function() {
			this.blobItemContainer_.bigger();
		}
	);

	this.eh_.listen(
		this.toolbar_, camlistore.Toolbar.EventType.SMALLER,
		function() {
			this.blobItemContainer_.smaller();
		}
	);

	this.eh_.listen(
		this.toolbar_, camlistore.Toolbar.EventType.ROOTS,
		function() {
			this.blobItemContainer_.showRoots(this.config_.signing);
		}
	);

	this.eh_.listen(
		this.toolbar_, camlistore.Toolbar.EventType.HOME,
		function() {
			window.location.href = "./index.html";
		}
	);

	this.eh_.listen(
		goog.dom.getElement('searchForm'),
		goog.events.EventType.SUBMIT,
		this.handleTextSearch_
	);

	this.eh_.listen(
		this.toolbar_, camlistore.Toolbar.EventType.CHECKED_ITEMS_CREATE_SET,
		function() {
			var blobItems = this.blobItemContainer_.getCheckedBlobItems();
			this.createNewSetWithItems_(blobItems);
		}
	);

	this.eh_.listen(
		this.toolbar_, camlistore.Toolbar.EventType.CHECKED_ITEMS_ADDTO_SET,
		function() {
			var blobItems = this.blobItemContainer_.getCheckedBlobItems();
			this.addItemsToSet_(blobItems);
		}
	);

	this.eh_.listen(
		this.blobItemContainer_,
		camlistore.BlobItemContainer.EventType.BLOB_ITEMS_CHOSEN,
		function() {
			this.handleItemSelection_(false);
		}
	);

	this.eh_.listen(
		this.blobItemContainer_,
		camlistore.BlobItemContainer.EventType.SINGLE_NODE_CHOSEN,
		function() {
			this.handleItemSelection_(true);
		}
	);

	this.eh_.listen(
		this.toolbar_, camlistore.Toolbar.EventType.SELECT_COLLEC,
		function() {
			var blobItems = this.blobItemContainer_.getCheckedBlobItems();
			// there should be only one item selected
			if (blobItems.length != 1) {
				alert("Select (only) one item to set as the default collection.");
				return;
			}
			this.blobItemContainer_.currentCollec_ = blobItems[0].blobRef_;
			this.blobItemContainer_.unselectAll();
			this.toolbar_.setCheckedBlobItemCount(0);
			this.toolbar_.toggleCollecButton(false);
			this.toolbar_.toggleAddToSetButton(false);
		}
	);

};


/**
 * @param {boolean} single Whether a single item has been (un)selected.
 * @private
 */
camlistore.SearchPage.prototype.handleItemSelection_ =
function(single) {
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
	if (single &&
		blobItems.length == 1 &&
		blobItems[0].isCollection()) {
		this.toolbar_.toggleCollecButton(true);
	} else {
		this.toolbar_.toggleCollecButton(false);
	}
}

// Returns true if the passed-in string might be a blobref.
isPlausibleBlobRef = function(blobRef) {
	return /^\w+-[a-f0-9]+$/.test(blobRef);
};

/**
 * @param {goog.events.Event} e The title form submit event.
 * @private
 */
camlistore.SearchPage.prototype.handleTextSearch_ =
function(e) {
	e.stopPropagation();
	e.preventDefault();

	var searchText = goog.dom.getElement("searchText");
	if (!!searchText && searchText.value == "") {
		return;
	}
	searchText.disabled = true;
	var btnSearch = goog.dom.getElement("btnSearch");
	btnSearch.disabled = true;

	var attr = "";
	var value = "";
	var fuzzy = false;
	if (searchText.value.indexOf(this.searchPrefix_.TAG) == 0) {
		// search by tag
		attr = "tag";
		value = searchText.value.slice(this.searchPrefix_.TAG.length);
	} else if (searchText.value.indexOf(this.searchPrefix_.TITLE) == 0) {
		// search by title
		attr = "title";
		value = searchText.value.slice(this.searchPrefix_.TITLE.length);
	} else if (searchText.value.indexOf(this.searchPrefix_.BLOBREF) == 0) {
		// or query directly by blobref (useful to get a permanode and set it
		// as the default collection)
		value = searchText.value.slice(this.searchPrefix_.BLOBREF.length);
		if (isPlausibleBlobRef(value)) {
			this.blobItemContainer_.findByBlobref_(value);
		}
		searchText.disabled = false;
		btnSearch.disabled = false;
		return;
	} else {
		// For when we support full text search again.
		attr = "";
		value = searchText.value;
		fuzzy = true;
	}

	this.blobItemContainer_.showWithAttr(this.config_.signing,
		attr, value, fuzzy, this.maxInResponse_
	);
	searchText.disabled = false;
	btnSearch.disabled = false;
};


/**
 * Called when component's element is known to have been removed from the
 * document.
 */
camlistore.SearchPage.prototype.exitDocument = function() {
	camlistore.SearchPage.superClass_.exitDocument.call(this);
	this.eh_.dispose();
};


/**
 * @param {Array.<camlistore.BlobItem>} blobItems Items to add to the permanode.
 * @private
 */
camlistore.SearchPage.prototype.createNewSetWithItems_ = function(blobItems) {
	this.connection_.createPermanode(
		goog.bind(this.addMembers_, this, true, blobItems));
};

/**
 * @param {Array.<camlistore.BlobItem>} blobItems Items to add to the permanode.
 * @private
 */
camlistore.SearchPage.prototype.addItemsToSet_ = function(blobItems) {
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
camlistore.SearchPage.prototype.addMembers_ =
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
			permanode, 'camliMember', blobItem.getBlobRef(), callback
		);
	}, this);
};


/**
 * @param {string} permanode Node to which the items were added.
 * @private
 */
camlistore.SearchPage.prototype.addItemsToSetDone_ = function(permanode) {
	this.blobItemContainer_.unselectAll();
	var blobItems = this.blobItemContainer_.getCheckedBlobItems();
	this.toolbar_.setCheckedBlobItemCount(blobItems.length);
	this.toolbar_.toggleCollecButton(false);
	this.toolbar_.toggleAddToSetButton(false);
};
