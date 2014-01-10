/*
Copyright 2014 Google Inc.

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

goog.require('goog.Uri');
goog.require('goog.events.EventTarget');
var assert = require('assert');

goog.require('cam.Navigator');

var MockLocation = function() {
	goog.base(this);
	this.href = '';
	this.reloadCount = 0;
};
goog.inherits(MockLocation, goog.events.EventTarget);
MockLocation.prototype.reload = function() {
	this.reloadCount++;
};

var MockHistory = function() {
	this.states = [];
};
MockHistory.prototype.pushState = function(a, b, url) {
	this.states.push(url);
};

var Handler = function() {
	this.lastURL = null;
	this.returnsTrue = false;
	this.handle = this.handle.bind(this);
};
Handler.prototype.handle = function(url) {
	this.lastURL = url;
	return this.returnsTrue;
};

describe('cam.Navigator', function() {
	var mockWindow, mockLocation, mockHistory, handler, navigator;
	var url = new goog.Uri('http://www.camlistore.org/foobar');

	beforeEach(function() {
		mockWindow = new goog.events.EventTarget();
		mockLocation = new MockLocation();
		mockHistory = new MockHistory();
		handler = new Handler();
		navigator = new cam.Navigator(mockWindow, mockLocation, mockHistory);
		navigator.onNavigate = handler.handle;
	});

	it('#navigate - no handler', function() {
		// We should do network navigation.
		navigator.onNavigate = function(){};
		navigator.navigate(url);
		assert.equal(mockLocation.href, url.toString());
		assert.equal(mockHistory.states.length, 0);
	});

	it('#navigate - handler returns false', function() {
		// Both handlers should get called, we should do network navigation.
		navigator.navigate(url);
		assert.equal(handler.lastURL, url);
		assert.equal(mockLocation.href, url.toString());
		assert.equal(mockHistory.states, 0);
	});

	it('#navigate - handler returns true', function() {
		// Both handlers should get called, we should do pushState() navigation.
		handler.returnsTrue = true;
		navigator.navigate(url);
		assert.equal(handler.lastURL, url);
		assert.equal(mockLocation.href, '');
		assert.deepEqual(mockHistory.states, [url.toString()]);
	});

	it('#handleClick_ - handled', function() {
		handler.returnsTrue = true;
		var ev = new goog.events.Event('click');
		ev.button = 0;
		ev.target = {
			nodeName: 'A',
			href: url.toString()
		};
		mockWindow.dispatchEvent(ev);
		assert.equal(mockLocation.href, '');
		assert.deepEqual(mockHistory.states, [url.toString()]);
	});

	it('#handleClick_ - not handled', function() {
		var ev = new goog.events.Event('click');
		ev.button = 0;
		ev.target = {
			nodeName: 'A',
			href: url.toString()
		};
		mockWindow.dispatchEvent(ev);
		assert.equal(mockLocation.href, '');
		assert.deepEqual(mockHistory.states, []);
		assert.equal(ev.defaultPrevented, false);
	});

	it('#handlePopState_ - handled', function() {
		handler.returnsTrue = true;
		mockWindow.dispatchEvent('popstate');
		assert.equal(mockLocation.reloadCount, 0);
		assert.deepEqual(mockHistory.states, []);
	});

	it('#handlePopState_ - not handled', function() {
		mockWindow.dispatchEvent('popstate');
		assert.equal(mockLocation.reloadCount, 1);
		assert.deepEqual(mockHistory.states, []);
	});
});
