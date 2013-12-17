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

goog.provide('camlistore.Nav');
goog.provide('camlistore.Nav.Item');

goog.require('camlistore.style');
goog.require('goog.dom');
goog.require('goog.dom.classes');
goog.require('goog.ui.Container');
goog.require('goog.ui.Component');
goog.require('goog.ui.Control');
goog.require('goog.ui.Button');

/**
 * A vertical, fixed-position expandy collapsy navigation bar thingy.
 */
camlistore.Nav = function(domHelper, opt_delegate) {
  goog.base(this, null, null, domHelper);

  this.delegate_ = opt_delegate;
  this.expandTimer_ = 0;
  this.collapseTimer_ = 0;
};
goog.inherits(camlistore.Nav, goog.ui.Container);

camlistore.Nav.prototype.createDom = function() {
  this.setElementInternal(this.dom_.createDom('div'));
  goog.dom.classes.add(this.element_, 'cam-nav');
  this.close();
};

camlistore.Nav.prototype.enterDocument = function() {
  goog.base(this, 'enterDocument');

  this.getHandler().listen(this.getElement(), goog.events.EventType.MOUSEOVER,
      this.handleMouseOver_);
  this.getHandler().listen(this.getElement(), goog.events.EventType.MOUSEOUT,
      this.handleMouseOut_);
  this.getHandler().listen(this.getElement(), goog.events.EventType.CLICK,
      this.toggle.bind(this));
};

camlistore.Nav.prototype.open = function() {
  if (this.delegate_) {
    this.delegate_.onNavOpen();
  }
  goog.dom.classes.remove(this.getElement(), 'cam-nav-collapsed');
};

camlistore.Nav.prototype.close = function() {
  if (this.delegate_) {
    this.delegate_.onNavClose();
  }

  goog.dom.classes.add(this.getElement(), 'cam-nav-collapsed');
};

camlistore.Nav.prototype.isOpen = function() {
  return !goog.dom.classes.has(this.getElement(), 'cam-nav-collapsed');
};

camlistore.Nav.prototype.toggle = function() {
  if (this.isOpen()) {
    this.close();
    return false;
  } else {
    this.open();
    return true;
  }
};

camlistore.Nav.prototype.handleMouseOver_ = function() {
  if (this.collapseTimer_) {
    window.clearTimeout(this.collapseTimer_);
    this.collapseTimer_ = 0;
  } else {
    this.expandTimer_ = window.setTimeout(function() {
      this.expandTimer_ = 0;
      this.open();
    }.bind(this), 250);
  }
};

camlistore.Nav.prototype.handleMouseOut_ = function() {
  if (this.expandTimer_) {
    window.clearTimeout(this.expandTimer_);
    this.expandTimer_ = 0;
  } else {
    if (this.isOpen()) {
      this.collapseTimer_ = window.setTimeout(function() {
        this.collapseTimer_ = 0;
        this.close();
      }.bind(this), 500);
    }
  }
};


camlistore.Nav.Item = function(domHelper, iconSrc, content) {
  goog.base(this, content, null, domHelper);
  this.iconSrc_ = iconSrc;
  this.addClassName('cam-nav-item');
};
goog.inherits(camlistore.Nav.Item, goog.ui.Button);

camlistore.Nav.Item.prototype.onClick = function() {};

camlistore.Nav.Item.prototype.createDom = function() {
  goog.base(this, 'createDom');
  this.setIcon(this.iconSrc_);
};

camlistore.Nav.Item.prototype.enterDocument = function() {
  this.getHandler().listen(this.getElement(), 'click', function(e) {
    this.onClick();
    e.stopPropagation();
  });
};

camlistore.Nav.Item.prototype.setIcon = function(src) {
  this.iconSrc_ = src;
  if (this.element_) {
    this.element_.style.backgroundImage = camlistore.style.getURLValue(src);
  }
};


camlistore.Nav.SearchItem = function(domHelper, iconSrc, label) {
  goog.base(this, domHelper, iconSrc, label);
  this.setAllowTextSelection(true);
  this.addClassName('cam-nav-searchitem');
};
goog.inherits(camlistore.Nav.SearchItem, camlistore.Nav.Item);

camlistore.Nav.SearchItem.prototype.onSearch = function(value) {};

camlistore.Nav.SearchItem.prototype.setText = function(text) {
  if (this.input_) {
    this.input_.value = text;
  }
};

camlistore.Nav.SearchItem.prototype.blur = function() {
  this.input_.blur();
};

camlistore.Nav.SearchItem.prototype.createDom = function() {
  this.setElementInternal(
      this.dom_.createDom('div', this.getExtraClassNames()));
  this.form_ = this.dom_.createDom('form');
  this.input_ = this.dom_.createDom(
      'input', {'placeholder': this.getContent()});
  this.form_.appendChild(this.input_);
  this.getElement().appendChild(this.form_);
  this.setIcon(this.iconSrc_);
};

camlistore.Nav.SearchItem.prototype.enterDocument = function() {
  goog.base(this, 'enterDocument');

  this.getHandler().listen(this.getElement(), 'mouseover',
                           this.input_.focus.bind(this.input_));

  this.getHandler().listen(this.getElement(), 'click', function(e) {
    this.input_.focus();
    e.stopPropagation();
  }.bind(this));

  this.getHandler().listen(this.form_, 'submit', function(e) {
    this.onSearch(this.input_.value);
    e.preventDefault();
  });
};

camlistore.Nav.SearchItem.prototype.handleKeyEvent = function(e) {
  return false;
};


camlistore.Nav.LinkItem = function(domHelper, iconSrc, label, linkUrl) {
  goog.base(this, domHelper, iconSrc, label);
  this.linkUrl_ = linkUrl;
  this.addClassName('cam-nav-linkitem');
};
goog.inherits(camlistore.Nav.LinkItem, camlistore.Nav.Item);

camlistore.Nav.LinkItem.prototype.createDom = function() {
  this.setElementInternal(
      this.dom_.createDom('a', this.getExtraClassNames(), this.getContent()));
  this.getElement().href = this.linkUrl_;
  this.setIcon(this.iconSrc_);
};

camlistore.Nav.LinkItem.prototype.enterDocument = function() {
  this.getHandler().listen(this.getElement(), 'click', function(e) {
    if (history.pushState) {
      history.pushState(null, '', this.getElement().href);
      e.preventDefault();
      e.stopPropagation();
    }
  });
};
