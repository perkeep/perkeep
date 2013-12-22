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
goog.require('goog.string');
goog.require('goog.Uri');
goog.require('goog.ui.Component');
goog.require('goog.ui.Textarea');
goog.require('camlistore.BlobItemContainer');
goog.require('camlistore.Nav');
goog.require('camlistore.ServerConnection');
goog.require('camlistore.ServerType');

camlistore.IndexPage = function(config, opt_domHelper) {
	goog.base(this, opt_domHelper);

	this.config_ = config;

	this.connection_ = new camlistore.ServerConnection(config);

	this.eh_ = new goog.events.EventHandler(this);

	// We have to store this because Firefox and Chrome disagree about whether to fire the popstate event at page load or not. Because of this we need to detect duplicate calls to handleUrl_().
	this.currentUrl_ = '';

	this.blobItemContainer_ = new camlistore.BlobItemContainer(this.connection_, opt_domHelper);
	this.blobItemContainer_.isSelectionEnabled = true;
	this.blobItemContainer_.isFileDragEnabled = true;

	this.nav_ = new camlistore.Nav(opt_domHelper, this);

	this.searchNavItem_ = new camlistore.Nav.SearchItem(this.dom_, 'magnifying_glass.svg', 'Search');
	this.newPermanodeNavItem_ = new camlistore.Nav.Item(this.dom_, 'new_permanode.svg', 'New permanode');
	this.searchRootsNavItem_ = new camlistore.Nav.Item(this.dom_, 'icon_27307.svg', 'Search roots');
	this.selectAsCurrentSetNavItem_ = new camlistore.Nav.Item(this.dom_, 'target.svg', 'Select as current set');
	this.selectAsCurrentSetNavItem_.setVisible(false);
	this.addToSetNavItem_ = new camlistore.Nav.Item(this.dom_, 'icon_16716.svg', 'Add to set');
	this.addToSetNavItem_.setVisible(false);
	this.createSetWithSelectionNavItem_ = new camlistore.Nav.Item(this.dom_, 'circled_plus.svg', 'Create set with 5 items');
	this.createSetWithSelectionNavItem_.setVisible(false);
	this.clearSelectionNavItem_ = new camlistore.Nav.Item(this.dom_, 'clear.svg', 'Clear selection');
	this.clearSelectionNavItem_.setVisible(false);
	this.embiggenNavItem_ = new camlistore.Nav.Item(this.dom_, 'up.svg', 'Moar bigger');
	this.ensmallenNavItem_ = new camlistore.Nav.Item(this.dom_, 'down.svg', 'Less bigger');
	this.logoNavItem_ = new camlistore.Nav.LinkItem(this.dom_, '/favicon.ico', 'Camlistore', '/ui/');
	this.logoNavItem_.addClassName('cam-logo');
};
goog.inherits(camlistore.IndexPage, goog.ui.Component);

camlistore.IndexPage.prototype.onNavOpen = function() {
	var currentWidth = this.getElement().offsetWidth - 36;
	var desiredWidth = currentWidth - (275 - 36);
	var scale = desiredWidth / currentWidth;

	var currentHeight = goog.dom.getDocumentHeight();
	var currentScroll = goog.dom.getDocumentScroll().y;
	var potentialScroll = currentHeight - goog.dom.getViewportSize().height;
	var originY = currentHeight * currentScroll / potentialScroll;
	console.log('origin y is: %f', originY);

	goog.style.setStyle(this.blobItemContainer_.getElement(),
		{'transform': goog.string.subs('scale(%s)', scale),
			'transform-origin': goog.string.subs('right %spx', originY)});
};

camlistore.IndexPage.prototype.onNavClose = function() {
	if (!this.blobItemContainer_.getElement()) {
		return;
	}
	this.searchNavItem_.setText('');
	this.searchNavItem_.blur();
	goog.style.setStyle(this.blobItemContainer_.getElement(),
		{'transform': ''});
};

camlistore.IndexPage.SEARCH_PREFIX_ = {
	RAW: 'raw'
};

camlistore.IndexPage.prototype.createDom = function() {
	this.decorateInternal(this.dom_.createElement('div'));
};

camlistore.IndexPage.prototype.decorateInternal = function(element) {
	camlistore.IndexPage.superClass_.decorateInternal.call(this, element);

	var el = this.getElement();
	goog.dom.classes.add(el, 'cam-index-page');

	document.title = this.config_.ownerName + '\'s Vault';

	this.nav_.addChild(this.searchNavItem_, true);
	this.nav_.addChild(this.newPermanodeNavItem_, true);
	this.nav_.addChild(this.searchRootsNavItem_, true);
	this.nav_.addChild(this.selectAsCurrentSetNavItem_, true);
	this.nav_.addChild(this.addToSetNavItem_, true);
	this.nav_.addChild(this.createSetWithSelectionNavItem_, true);
	this.nav_.addChild(this.clearSelectionNavItem_, true);
	this.nav_.addChild(this.embiggenNavItem_, true);
	this.nav_.addChild(this.ensmallenNavItem_, true);
	this.nav_.addChild(this.logoNavItem_, true);

	this.addChild(this.nav_, true);
	this.addChild(this.blobItemContainer_, true);
};

camlistore.IndexPage.prototype.updateNavButtonsForSelection_ = function() {
	var blobItems = this.blobItemContainer_.getCheckedBlobItems();
	var count = blobItems.length;

	if (count) {
		var txt = 'Create set with ' + count + ' item' + (count > 1 ? 's' : '');
		this.createSetWithSelectionNavItem_.setContent(txt);
		this.createSetWithSelectionNavItem_.setVisible(true);
		this.clearSelectionNavItem_.setVisible(true);
	} else {
		this.createSetWithSelectionNavItem_.setContent('');
		this.createSetWithSelectionNavItem_.setVisible(false);
		this.clearSelectionNavItem_.setVisible(false);
	}

	if (this.blobItemContainer_.currentCollec_ && this.blobItemContainer_.currentCollec_ != "" && blobItems.length > 0) {
		this.addToSetNavItem_.setVisible(true);
	} else {
		this.addToSetNavItem_.setVisible(false);
	}

	if (blobItems.length == 1 && blobItems[0].isCollection()) {
		this.selectAsCurrentSetNavItem_.setVisible(true);
	} else {
		this.selectAsCurrentSetNavItem_.setVisible(false);
	}
};

camlistore.IndexPage.prototype.disposeInternal = function() {
	camlistore.IndexPage.superClass_.disposeInternal.call(this);
	this.eh_.dispose();
};

camlistore.IndexPage.prototype.enterDocument = function() {
	camlistore.IndexPage.superClass_.enterDocument.call(this);

	this.connection_.serverStatus(goog.bind(function(resp) {
		this.handleServerStatus_(resp);
	}, this));

	this.eh_.listen(window, goog.events.EventType.POPSTATE, this.handleUrl_);

	this.searchNavItem_.onSearch = this.setUrlSearch_.bind(this);

	this.embiggenNavItem_.onClick = function() {
		if (this.blobItemContainer_.bigger()) {
			var force = true;
			this.blobItemContainer_.layout_(force);
		}
	}.bind(this);

	this.ensmallenNavItem_.onClick = function() {
		if (this.blobItemContainer_.smaller()) {
			// Don't run a query. Let the browser do the image resizing on its own.
			var force = true;
			this.blobItemContainer_.layout_(force);
			// Since things got smaller, we may need to fetch more content.
			this.blobItemContainer_.handleScroll_();
		}
	}.bind(this);

	this.createSetWithSelectionNavItem_.onClick = function() {
		var blobItems = this.blobItemContainer_.getCheckedBlobItems();
		this.createNewSetWithItems_(blobItems);
	}.bind(this);

	this.clearSelectionNavItem_.onClick = this.blobItemContainer_.unselectAll.bind(this.blobItemContainer_);

	this.newPermanodeNavItem_.onClick = function() {
		this.connection_.createPermanode(function(p) {
			window.location = './?p=' + p;
		}, function(failMsg) {
			console.error('Failed to create permanode: ' + failMsg);
		});
	}.bind(this);

	this.addToSetNavItem_.onClick = function() {
		var blobItems = this.blobItemContainer_.getCheckedBlobItems();
		this.addItemsToSet_(blobItems);
	}.bind(this);

	this.selectAsCurrentSetNavItem_.onClick = function() {
		var blobItems = this.blobItemContainer_.getCheckedBlobItems();
		// there should be only one item selected
		if (blobItems.length != 1) {
			alert("Cannet set multiple items as current collection");
			return;
		}
		this.blobItemContainer_.currentCollec_ = blobItems[0].blobRef_;
		this.blobItemContainer_.unselectAll();
		this.updateNavButtonsForSelection_();
	}.bind(this);

	this.searchRootsNavItem_.onClick = this.setUrlSearch_.bind(this, {
		permanode: {
			attr: 'camliRoot',
			numValue: {
				min: 1
			}
		}
	});

	this.eh_.listen(this.blobItemContainer_, camlistore.BlobItemContainer.EventType.SELECTION_CHANGED, this.updateNavButtonsForSelection_.bind(this));

	this.eh_.listen(this.getElement(), 'keypress', function(e) {
		if (String.fromCharCode(e.charCode) == '/') {
			this.nav_.open();
			this.searchNavItem_.focus();
			e.preventDefault();
		}
	});

	this.handleUrl_();
};

camlistore.IndexPage.prototype.exitDocument = function() {
	camlistore.IndexPage.superClass_.exitDocument.call(this);
	// Clear event handlers here
};

camlistore.IndexPage.prototype.createNewSetWithItems_ = function(blobItems) {
	this.connection_.createPermanode(goog.bind(this.addMembers_, this, true, blobItems));
};

camlistore.IndexPage.prototype.addItemsToSet_ = function(blobItems) {
	if (!this.blobItemContainer_.currentCollec_ || this.blobItemContainer_.currentCollec_ == "") {
		alert("no destination collection selected");
	}
	this.addMembers_(false, blobItems, this.blobItemContainer_.currentCollec_);
};

camlistore.IndexPage.prototype.addMembers_ = function(newSet, blobItems, permanode) {
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
		this.connection_.newSetAttributeClaim(permanode, 'title', 'My new set', function() {});
	}
	goog.array.forEach(blobItems, function(blobItem, index) {
		this.connection_.newAddAttributeClaim(permanode, 'camliMember', blobItem.getBlobRef(), callback);
	}, this);
};

camlistore.IndexPage.prototype.addItemsToSetDone_ = function(permanode) {
	this.blobItemContainer_.unselectAll();
	this.updateNavButtonsForSelection_();
	this.blobItemContainer_.showRecent();
};

camlistore.IndexPage.prototype.handleServerStatus_ = function(resp) {
	if (resp && resp.version) {
		// TODO(aa): Argh
		//this.toolbar_.setStatus('v' + resp.version);
	}
};

camlistore.IndexPage.prototype.setUrlSearch_ = function(search) {
	var searchText = goog.isString(search) ? goog.string.trim(search) :
		goog.string.subs('%s:%s', this.constructor.SEARCH_PREFIX_.RAW, JSON.stringify(search));
	var uri = new goog.Uri(location.href);
	uri.setParameterValue('q', searchText);
	if (history.pushState) {
		history.pushState(null, '', uri.toString());
		this.handleUrl_();
	} else {
		location.href = uri.toString();
	}
};

camlistore.IndexPage.prototype.handleUrl_ = function() {
	if (location.href == this.currentUrl_) {
		console.log('Dropping duplicate handleUrl_ for %s', this.currentUrl_);
		return;
	}
	this.currentUrl_ = location.href;

	var uri = new goog.Uri(location.href);
	var query = uri.getParameterValue('q');
	if (!query) {
		this.blobItemContainer_.showRecent();
		return;
	}

	// TODO(aa): Remove this when the server can do something like the 'raw' operator.
	if (goog.string.startsWith(query, this.constructor.SEARCH_PREFIX_.RAW + ':')) {
		query = JSON.parse(query.substring(this.constructor.SEARCH_PREFIX_.RAW.length + 1));
	}

	this.blobItemContainer_.search(query);
};
