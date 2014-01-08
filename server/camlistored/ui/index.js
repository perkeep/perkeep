/*
Copyright 2012 Google Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

goog.provide('cam.IndexPage');

goog.require('goog.array');
goog.require('goog.dom');
goog.require('goog.dom.classes');
goog.require('goog.events.EventHandler');
goog.require('goog.events.EventType');
goog.require('goog.events.KeyCodes');
goog.require('goog.string');
goog.require('goog.Uri');
goog.require('goog.ui.Component');
goog.require('goog.ui.Textarea');

goog.require('cam.AnimationLoop');
goog.require('cam.BlobItemContainer');
goog.require('cam.DetailView');
goog.require('cam.object');
goog.require('cam.Nav');
goog.require('cam.SearchSession');
goog.require('cam.ServerConnection');
goog.require('cam.ServerType');

cam.IndexPage = function(config, opt_domHelper) {
	goog.base(this, opt_domHelper);

	this.config_ = config;

	this.connection_ = new cam.ServerConnection(config);

	this.eh_ = new goog.events.EventHandler(this);

	// We have to store this because Firefox and Chrome disagree about whether to fire the popstate event at page load or not. Because of this we need to detect duplicate calls to handleUrl_().
	this.currentUri_ = null;

	this.nav_ = new cam.Nav(opt_domHelper, this);

	this.searchNavItem_ = new cam.Nav.SearchItem(this.dom_, 'magnifying_glass.svg', 'Search');
	this.newPermanodeNavItem_ = new cam.Nav.Item(this.dom_, 'new_permanode.svg', 'New permanode');
	this.searchRootsNavItem_ = new cam.Nav.Item(this.dom_, 'icon_27307.svg', 'Search roots');
	this.selectAsCurrentSetNavItem_ = new cam.Nav.Item(this.dom_, 'target.svg', 'Select as current set');
	this.selectAsCurrentSetNavItem_.setVisible(false);
	this.addToSetNavItem_ = new cam.Nav.Item(this.dom_, 'icon_16716.svg', 'Add to set');
	this.addToSetNavItem_.setVisible(false);
	this.createSetWithSelectionNavItem_ = new cam.Nav.Item(this.dom_, 'circled_plus.svg', 'Create set with 5 items');
	this.createSetWithSelectionNavItem_.setVisible(false);
	this.clearSelectionNavItem_ = new cam.Nav.Item(this.dom_, 'clear.svg', 'Clear selection');
	this.clearSelectionNavItem_.setVisible(false);
	this.embiggenNavItem_ = new cam.Nav.Item(this.dom_, 'up.svg', 'Moar bigger');
	this.ensmallenNavItem_ = new cam.Nav.Item(this.dom_, 'down.svg', 'Less bigger');
	this.logoNavItem_ = new cam.Nav.LinkItem(this.dom_, '/favicon.ico', 'Camlistore', '/ui/');
	this.logoNavItem_.addClassName('cam-logo');

	this.searchSession_ = null;

	this.blobItemContainer_ = new cam.BlobItemContainer(this.connection_, opt_domHelper);
	this.blobItemContainer_.isSelectionEnabled = true;
	this.blobItemContainer_.isFileDragEnabled = true;

	// TODO(aa): This is a quick hack to make the scroll position restore in the case where you go to detail view, then press back to search page.
	// To make the reload case work we need to save the scroll position in window.history. That needs more thought though, we might want to store something more abstract that the scroll position.
	this.savedScrollPosition_ = 0;

	this.detail_ = null;
	this.detailLoop_ = null;
	this.detailViewHost_ = null;
};
goog.inherits(cam.IndexPage, goog.ui.Component);

cam.IndexPage.prototype.onNavOpen = function() {
	this.setTransform_();
};

cam.IndexPage.prototype.setTransform_ = function() {
	var currentWidth = this.getElement().offsetWidth - 36;
	var desiredWidth = currentWidth - (275 - 36);
	var scale = desiredWidth / currentWidth;

	var currentHeight = goog.dom.getDocumentHeight();
	var currentScroll = goog.dom.getDocumentScroll().y;
	var potentialScroll = currentHeight - goog.dom.getViewportSize().height;
	var originY = currentHeight * currentScroll / potentialScroll;

	goog.style.setStyle(this.blobItemContainer_.getElement(),
		{'transform': goog.string.subs('scale(%s)', scale),
			'transform-origin': goog.string.subs('right %spx', originY)});
};

cam.IndexPage.prototype.onNavClose = function() {
	if (!this.blobItemContainer_.getElement()) {
		return;
	}
	this.searchNavItem_.setText('');
	this.searchNavItem_.blur();
	goog.style.setStyle(this.blobItemContainer_.getElement(),
		{'transform': ''});
};

cam.IndexPage.SEARCH_PREFIX_ = {
	RAW: 'raw'
};

cam.IndexPage.prototype.createDom = function() {
	this.decorateInternal(this.dom_.createElement('div'));
};

cam.IndexPage.prototype.decorateInternal = function(element) {
	cam.IndexPage.superClass_.decorateInternal.call(this, element);

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

	this.detailViewHost_ = this.dom_.createElement('div');

	this.addChild(this.nav_, true);
	this.addChild(this.blobItemContainer_, true);
	el.appendChild(this.detailViewHost_);
};

cam.IndexPage.prototype.updateNavButtonsForSelection_ = function() {
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

cam.IndexPage.prototype.disposeInternal = function() {
	cam.IndexPage.superClass_.disposeInternal.call(this);
	this.eh_.dispose();
};

cam.IndexPage.prototype.enterDocument = function() {
	cam.IndexPage.superClass_.enterDocument.call(this);

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

	this.logoNavItem_.onClick = this.navigate_.bind(this);

	this.eh_.listen(this.blobItemContainer_, cam.BlobItemContainer.EventType.SELECTION_CHANGED, this.updateNavButtonsForSelection_.bind(this));

	// TODO(aa): We need to implement general purpose routing and get rid of all these one-off hacks.
	this.eh_.listen(this.getElement(), 'click', function(e) {
		if (e.target.className == 'cam-blobitem-thumb') {
			var uri = new goog.Uri(this.dom_.getAncestorByTagNameAndClass(e.target, 'a').href);
			if (uri.getParameterValue('newui') == '1') {
				try {
					this.navigate_(uri.toString());
				} finally {
					e.preventDefault();
				}
			}
		}
	});

	this.eh_.listen(this.getElement(), 'keypress', function(e) {
		if (String.fromCharCode(e.charCode) == '/') {
			this.nav_.open();
			this.searchNavItem_.focus();
			e.preventDefault();
		}
	});

	this.eh_.listen(this.getElement(), 'keyup', function(e) {
		if (!this.detail_) {
			return;
		}

		if (e.keyCode == goog.events.KeyCodes.LEFT) {
			this.detail_.navigate(-1);
		} else if (e.keyCode == goog.events.KeyCodes.RIGHT) {
			this.detail_.navigate(1);
		}
	});

	this.handleUrl_();
};

cam.IndexPage.prototype.exitDocument = function() {
	cam.IndexPage.superClass_.exitDocument.call(this);
	// Clear event handlers here
};

cam.IndexPage.prototype.createNewSetWithItems_ = function(blobItems) {
	this.connection_.createPermanode(goog.bind(this.addMembers_, this, true, blobItems));
};

cam.IndexPage.prototype.addItemsToSet_ = function(blobItems) {
	if (!this.blobItemContainer_.currentCollec_ || this.blobItemContainer_.currentCollec_ == "") {
		alert("no destination collection selected");
	}
	this.addMembers_(false, blobItems, this.blobItemContainer_.currentCollec_);
};

cam.IndexPage.prototype.addMembers_ = function(newSet, blobItems, permanode) {
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

cam.IndexPage.prototype.addItemsToSetDone_ = function(permanode) {
	this.blobItemContainer_.unselectAll();
	this.updateNavButtonsForSelection_();
	this.setUrlSearch_(' ');
};

cam.IndexPage.prototype.handleServerStatus_ = function(resp) {
	if (resp && resp.version) {
		// TODO(aa): Argh
		//this.toolbar_.setStatus('v' + resp.version);
	}
};

cam.IndexPage.prototype.setUrlSearch_ = function(search) {
	var searchText = goog.isString(search) ? goog.string.trim(search) :
		goog.string.subs('%s:%s', this.constructor.SEARCH_PREFIX_.RAW, JSON.stringify(search));
	var uri = new goog.Uri(location.href);
	uri.setParameterValue('q', searchText);
	this.navigate_(uri.toString());
};

cam.IndexPage.prototype.navigate_ = function(url) {
	if (history.pushState) {
		history.pushState(null, '', url);
		this.handleUrl_();
	} else {
		location.href = url;
	}
};

cam.IndexPage.prototype.handleUrl_ = function() {
	var newUri = new goog.Uri(location.href);
	if (this.currentUri_ != null && this.currentUri_.toString() == newUri.toString()) {
		console.log('Dropping duplicate handleUrl_ for %s', newUri.toString());
		return;
	}
	this.currentUri_ = newUri;

	this.updateSearchSession_();
	this.updateSearchView_();
	this.updateDetailView_();
};

cam.IndexPage.prototype.updateSearchSession_ = function() {
	var query = this.currentUri_.getParameterValue('q');
	if (!query) {
		query = ' ';
	}

	// TODO(aa): Remove this when the server can do something like the 'raw' operator.
	if (goog.string.startsWith(query, this.constructor.SEARCH_PREFIX_.RAW + ':')) {
		query = JSON.parse(query.substring(this.constructor.SEARCH_PREFIX_.RAW.length + 1));
	}

	if (this.searchSession_ && JSON.stringify(this.searchSession_.getQuery()) == JSON.stringify(query)) {
		return;
	}

	if (this.searchSession_) {
		this.searchSession_.close();
	}

	this.searchSession_ = new cam.SearchSession(this.connection_, new goog.Uri(location.href), query);
};

cam.IndexPage.prototype.updateSearchView_ = function() {
	if (this.inDetailMode_()) {
		this.savedScrollPosition_ = goog.dom.getDocumentScroll().y;
		this.blobItemContainer_.setVisible(false);
		return;
	}

	if (!this.blobItemContainer_.isVisible()) {
		this.blobItemContainer_.setVisible(true);
		goog.dom.getDocumentScrollElement().scrollTop = this.savedScrollPosition_;
	}

	if (this.nav_.isOpen()) {
		this.setTransform_();
	}

	this.blobItemContainer_.showSearchSession(this.searchSession_);
};

cam.IndexPage.prototype.updateDetailView_ = function() {
	if (!this.inDetailMode_()) {
		if (this.detail_) {
			this.detailLoop_.stop();
			React.unmountComponentAtNode(this.detailViewHost_);
			this.detailLoop_ = null;
			this.detail_ = null;
		}
		return;
	}

	var props = {
		blobref: this.currentUri_.getParameterValue('p'),
		searchSession: this.searchSession_,
		onNavigate: this.handleDetailNaviate_.bind(this)
	}

	if (this.detail_) {
		this.detail_.setProps(props);
		return;
	}

	var lastWidth = window.innerWidth;
	var lastHeight = window.innerHeight;

	this.detail_ = cam.DetailView(cam.object.extend(props, {
		width: lastWidth,
		height: lastHeight
	}));
	React.renderComponent(this.detail_, this.detailViewHost_);

	this.detailLoop_ = new cam.AnimationLoop(window);
	this.detailLoop_.addEventListener('frame', function() {
		if (window.innerWidth != lastWidth || window.innerHeight != lastHeight) {
			lastWidth = window.innerWidth;
			lastHeight = window.innerHeight;
			this.detail_.setProps({width:lastWidth, height:lastHeight});
		}
	}.bind(this));
	this.detailLoop_.start();
};

cam.IndexPage.prototype.handleDetailNaviate_ = function(blobref) {
	var uri = new goog.Uri(this.currentUri_);
	uri.setParameterValue('p', blobref);
	this.navigate_(uri.toString());
};

cam.IndexPage.prototype.inDetailMode_ = function() {
	return this.currentUri_.getParameterValue('newui') == '1';
};
