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

goog.provide('cam.Navigator');

goog.require('cam.object');
goog.require('goog.Uri');

// Navigator intercepts various types of browser navgiations and gives its client an opportunity to decide whether the navigation should be handled with JavaScript or not.
// Currently, 'click' events on hyperlinks and 'popstate' events are intercepted. Clients can also call navigate() to manually initiate navigation.
//
// @param Window win The window to listen for click and popstate events within to potentially interpret as navigations.
// @param Location location Network navigation will be executed using this location object.
// @param History history PushState navigation will be executed using this history object.
cam.Navigator = function(win, location, history) {
	this.win_ = win;
	this.location_ = location;
	this.history_ = history;
	this.handlers_ = [];

	// This is needed so that in handlePopState_, we can differentiate navigating back to this frame from the initial load.
	// We can't just initialize to {} because there can already be interesting state (e.g., in the case of the user pressing the refresh button).
	history.replaceState(cam.object.extend(history.state), '', location.href);

	this.win_.addEventListener('click', this.handleClick_.bind(this));
	this.win_.addEventListener('popstate', this.handlePopState_.bind(this));
};

cam.Navigator.shouldHandleClick = function(e) {
	// We are conservative and only try to handle left clicks that are unmodified.
	// For any other kind of click, assume that something fancy (e.g., context menu, open in new tab, etc) is about to happen and let whatever it happen as normal.
	if (e.button != 0 || e.altKey || e.ctrlKey || e.metaKey || e.shiftKey) {
		return null;
	}

	for (var elm = e.target; ; elm = elm.parentElement) {
		if (!elm) {
			return null;
		}
		if (elm.nodeName == 'A' && elm.href) {
			return elm;
		}
	}

	throw new Error('Should never get here');
	return null;
};

// Client should set this to handle navigation.
//
// This is called before the navigation has actually taken place: location.href will refer to the old URL, not the new one. Also, history.state will refer to previous state.
//
// If client returns true, then Navigator considers the navigation handled locally, and will add an entry to history using pushState(). If this method returns false, Navigator lets the navigation fall through to the browser.
// @param goog.Uri newURL The URL to navigate to.
// @return boolean Whether the navigation was handled locally.
cam.Navigator.prototype.onWillNavigate = function(newURL) {};

// Called after a local (pushState) navigation has been performed. At this point, location.href and history.state have been updated.
cam.Navigator.prototype.onDidNavigate = function() {};

// Programmatically initiate a navigation to a URL. Useful for triggering navigations from things other than hyperlinks.
// @param goog.Uri url The URL to navigate to.
// @return boolean Whether the navigation was handled locally.
cam.Navigator.prototype.navigate = function(url) {
	if (this.dispatchImpl_(url, true)) {
		return true;
	}
	this.location_.href = url.toString();
	return false;
};

// Handles navigations initiated via clicking a hyperlink.
cam.Navigator.prototype.handleClick_ = function(e) {
	var elm = cam.Navigator.shouldHandleClick(e);
	if (!elm) {
		return;
	}

	try {
		if (this.dispatchImpl_(new goog.Uri(elm.href), true)) {
			e.preventDefault();
		}
	} catch (ex) {
		// Prevent the navigation so that we can see the error.
		e.preventDefault();
		throw ex;
	}
	// Otherwise, the event continues bubbling and navigation should happen as normal via the browser.
};

// Handles navigation via popstate.
cam.Navigator.prototype.handlePopState_ = function(e) {
	// WebKit and older Chrome versions will fire a spurious initial popstate event after load.
	// We can differentiate this event from ones corresponding to frames we generated ourselves with pushState() or replaceState() because our own frames always have a non-empty state.
	// See: http://stackoverflow.com/questions/6421769/popstate-on-pages-load-in-chrome
	if (!e.state) {
		return;
	}
	if (!this.dispatchImpl_(new goog.Uri(this.location_.href), false)) {
		this.location_.reload();
	}
};

cam.Navigator.prototype.dispatchImpl_ = function(url, addState) {
	if (this.onWillNavigate(url)) {
		if (addState) {
			// Pass an empty object rather than null or undefined so that we can filter out spurious initial popstate events in handlePopState_.
			this.history_.pushState({}, '', url.toString());
		}
		this.onDidNavigate();
		return true;
	}
	return false;
};
