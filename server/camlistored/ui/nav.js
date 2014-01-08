/*
Copyright 2013 The Camlistore Authors.

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

goog.provide('cam.Nav');
goog.provide('cam.Nav.Item');

goog.require('cam.style');
goog.require('goog.dom');
goog.require('goog.dom.classes');
goog.require('goog.events.KeyCodes');
goog.require('goog.ui.Container');
goog.require('goog.ui.Component');
goog.require('goog.ui.Control');
goog.require('goog.ui.Button');

// A vertical, fixed-position expandy collapsy navigation bar thingy.
cam.Nav = function(domHelper, opt_delegate) {
	goog.base(this, null, null, domHelper);

	this.delegate_ = opt_delegate;
	this.expandTimer_ = 0;
};
goog.inherits(cam.Nav, goog.ui.Container);

cam.Nav.prototype.createDom = function() {
	this.setElementInternal(this.dom_.createDom('div'));
	goog.dom.classes.add(this.element_, 'cam-nav');

	this.closeButton_ = this.dom_.createDom('img', 'cam-nav-close');
	this.closeButton_.src = 'close.svg';
	this.getElement().appendChild(this.closeButton_);

	this.close();
};

cam.Nav.prototype.enterDocument = function() {
	goog.base(this, 'enterDocument');

	this.getHandler().listen(this.getElement(), 'mouseover', this.handleMouseOver_);
	this.getHandler().listen(this.getElement(), 'mouseout', this.handleMouseOut_);

	this.getHandler().listen(this.closeButton_, 'click', function(e) {
		e.stopPropagation();
		this.close();
	}.bind(this));

	this.getHandler().listen(this.getElement(), 'keyup', function(e) {
		if (e.keyCode == goog.events.KeyCodes.ESC) {
			this.close();
			e.preventDefault();
		}
	});
};

cam.Nav.prototype.open = function() {
	if (this.delegate_) {
		this.delegate_.onNavOpen();
	}
	goog.dom.classes.remove(this.getElement(), 'cam-nav-collapsed');
};

cam.Nav.prototype.close = function() {
	if (this.delegate_) {
		this.delegate_.onNavClose();
	}

	goog.dom.classes.add(this.getElement(), 'cam-nav-collapsed');
};

cam.Nav.prototype.isOpen = function() {
	return !goog.dom.classes.has(this.getElement(), 'cam-nav-collapsed');
};

cam.Nav.prototype.toggle = function() {
	if (this.isOpen()) {
		this.close();
		return false;
	} else {
		this.open();
		return true;
	}
};

cam.Nav.prototype.handleMouseOver_ = function() {
	this.expandTimer_ = window.setTimeout(function() {
		this.expandTimer_ = 0;
		this.open();
	}.bind(this), 250);
};

cam.Nav.prototype.handleMouseOut_ = function() {
	if (this.expandTimer_) {
		window.clearTimeout(this.expandTimer_);
		this.expandTimer_ = 0;
	}
};


cam.Nav.Item = function(domHelper, iconSrc, content) {
	goog.base(this, content, null, domHelper);
	this.iconSrc_ = iconSrc;
	this.addClassName('cam-nav-item');
};
goog.inherits(cam.Nav.Item, goog.ui.Button);

cam.Nav.Item.prototype.onClick = function() {};

cam.Nav.Item.prototype.createDom = function() {
	goog.base(this, 'createDom');
	this.setIcon(this.iconSrc_);
};

cam.Nav.Item.prototype.enterDocument = function() {
	this.getHandler().listen(this.getElement(), 'click', function(e) {
		this.onClick();
		e.stopPropagation();
	});
};

cam.Nav.Item.prototype.setIcon = function(src) {
	this.iconSrc_ = src;
	if (this.element_) {
		this.element_.style.backgroundImage = cam.style.getURLValue(src);
	}
};


cam.Nav.SearchItem = function(domHelper, iconSrc, label) {
	goog.base(this, domHelper, iconSrc, label);
	this.setAllowTextSelection(true);
	this.addClassName('cam-nav-searchitem');
};
goog.inherits(cam.Nav.SearchItem, cam.Nav.Item);

cam.Nav.SearchItem.prototype.onSearch = function(value) {};

cam.Nav.SearchItem.prototype.setText = function(text) {
	if (this.input_) {
		this.input_.value = text;
	}
};

cam.Nav.SearchItem.prototype.focus = function() {
	this.input_.focus();
};

cam.Nav.SearchItem.prototype.blur = function() {
	this.input_.blur();
};

cam.Nav.SearchItem.prototype.createDom = function() {
	this.setElementInternal(this.dom_.createDom('div', this.getExtraClassNames()));
	this.form_ = this.dom_.createDom('form');
	this.input_ = this.dom_.createDom('input', {'placeholder': this.getContent()});
	this.form_.appendChild(this.input_);
	this.getElement().appendChild(this.form_);
	this.setIcon(this.iconSrc_);
};

cam.Nav.SearchItem.prototype.enterDocument = function() {
	goog.base(this, 'enterDocument');

	this.getHandler().listen(this.input_, 'mouseover', this.input_.focus.bind(this.input_));

	this.getHandler().listen(this.getElement(), 'click', function(e) {
		this.input_.focus();
		e.stopPropagation();
	}.bind(this));

	this.getHandler().listen(this.form_, 'submit', function(e) {
		this.onSearch(this.input_.value);
		e.preventDefault();
	});
};


cam.Nav.LinkItem = function(domHelper, iconSrc, label, linkUrl) {
	goog.base(this, domHelper, iconSrc, label);
	this.linkUrl_ = linkUrl;
	this.addClassName('cam-nav-linkitem');
};
goog.inherits(cam.Nav.LinkItem, cam.Nav.Item);

cam.Nav.LinkItem.prototype.onClick = function(url) {};

cam.Nav.LinkItem.prototype.createDom = function() {
	this.setElementInternal(this.dom_.createDom('a', this.getExtraClassNames(), this.getContent()));
	this.getElement().href = this.linkUrl_;
	this.setIcon(this.iconSrc_);
};

cam.Nav.LinkItem.prototype.enterDocument = function() {
	this.getHandler().listen(this.getElement(), 'click', function(e) {
		this.onClick(this.linkUrl_);
		e.preventDefault();
		e.stopPropagation();
	});
};
