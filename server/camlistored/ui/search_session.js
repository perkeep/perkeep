/*
Copyright 2013 Google Inc.

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

goog.provide('cam.SearchSession');

goog.require('goog.events.EventTarget');
goog.require('goog.Uri');
goog.require('goog.Uri.QueryData');
goog.require('goog.uri.utils');

goog.require('cam.ServerConnection');

// A search session is a standing query that notifies you when results change. It caches previous results and handles merging new data as it is received. It does not tell you _what_ changed; clients must reconcile as they see fit.
//
// TODO(aa): Only deltas should be sent from server to client
// TODO(aa): Need some way to avoid the duplicate query when websocket starts. Ideas:
// - Initial XHR query can also specify tag. This tag times out if not used rapidly. Send this same tag in socket query.
// - Socket assumes that client already has first batch of results (slightly racey though)
// - Prefer to use socket on client-side, test whether it works and fall back to XHR if not.
cam.SearchSession = function(connection, currentUri, query) {
	goog.base(this);

	this.connection_ = connection;
	this.initSocketUri_(currentUri);
	this.query_ = query;
	this.instance_ = this.constructor.instanceCount_++;
	this.continuation_ = this.getContinuation_(this.constructor.SEARCH_SESSION_CHANGE_TYPE.NEW);
	this.socket_ = null;
	this.supportsWebSocket_ = false;

	this.resetData_();
};
goog.inherits(cam.SearchSession, goog.events.EventTarget);

// We fire this event when the data changes in any way.
cam.SearchSession.SEARCH_SESSION_CHANGED = 'search-session-change';

// TODO(aa): This is only used by BlobItemContainer. Once we switch over to BlobItemContainerReact completely, it can be removed.
cam.SearchSession.SEARCH_SESSION_CHANGE_TYPE = {
	NEW: 1,
	APPEND: 2,
	UPDATE: 3
};

cam.SearchSession.prototype.PAGE_SIZE_ = 50;

cam.SearchSession.DESCRIBE_REQUEST = {
	// This size doesn't matter, we don't use it. We only care about the aspect ratio.
	// TODO(aa): This needs to die: https://code.google.com/p/camlistore/issues/detail?id=321
	thumbnailSize: 1000,

	// TODO(aa): This is not great. The describe request will still return tons of data we don't care about:
	// - Children of folders
	// - Properties we don't use
	// See: https://code.google.com/p/camlistore/issues/detail?id=319
	depth: 2
};

cam.SearchSession.instanceCount_ = 0;

cam.SearchSession.prototype.getQuery = function() {
	return this.query_;
}

// Returns all the data we currently have loaded.
// It is guaranteed to return the following properties:
// blobs // non-zero length
// description
// description.meta
cam.SearchSession.prototype.getCurrentResults = function() {
	return this.data_;
};

// Loads the next page of data. This is safe to call while a load is in progress; multiple calls for the same page will be collapsed. The SEARCH_SESSION_CHANGED event will be dispatched when the new data is available.
cam.SearchSession.prototype.loadMoreResults = function() {
	if (!this.continuation_) {
		return;
	}

	var c = this.continuation_;
	this.continuation_ = null;
	c();
};

// Returns true if it is known that all data which can be loaded for this query has been.
cam.SearchSession.prototype.isComplete = function() {
	return !this.continuation_;
}

cam.SearchSession.prototype.supportsChangeNotifications = function() {
	return this.supportsWebSocket_;
};

cam.SearchSession.prototype.refreshIfNecessary = function() {
	if (this.supportsWebSocket_) {
		return;
	}

	this.continuation_ = this.getContinuation_(this.constructor.SEARCH_SESSION_CHANGE_TYPE.UPDATE, null, Math.max(this.data_.blobs.length, this.constructor.PAGE_SIZE_));
	this.resetData_();
	this.loadMoreResults();
};

cam.SearchSession.prototype.close = function() {
	if (this.socket_) {
		this.socket_.close();
	}
};

cam.SearchSession.prototype.resetData_ = function() {
	this.data_ = {
		blobs: [],
		description: {
			meta: {}
		}
	};
};

cam.SearchSession.prototype.initSocketUri_ = function(currentUri) {
	if (!goog.global.WebSocket) {
		return;
	}

	this.socketUri_ = currentUri;
	var config = this.connection_.getConfig();
	this.socketUri_.setPath(goog.uri.utils.appendPath(config.searchRoot, 'camli/search/ws'));
	this.socketUri_.setQuery(goog.Uri.QueryData.createFromMap({authtoken: config.wsAuthToken || ''}));
	if (this.socketUri_.getScheme() == "https") {
		this.socketUri_.setScheme("wss");
	} else {
		this.socketUri_.setScheme("ws");
	}
};

cam.SearchSession.prototype.getContinuation_ = function(changeType, opt_continuationToken, opt_limit) {
	return this.connection_.search.bind(this.connection_, this.query_, this.constructor.DESCRIBE_REQUEST, opt_limit || this.PAGE_SIZE_, opt_continuationToken,
		this.searchDone_.bind(this, changeType));
};

cam.SearchSession.prototype.searchDone_ = function(changeType, result) {
	if (changeType == this.constructor.SEARCH_SESSION_CHANGE_TYPE.APPEND) {
		this.data_.blobs = this.data_.blobs.concat(result.blobs);
		goog.mixin(this.data_.description.meta, result.description.meta);
	} else {
		this.data_.blobs = result.blobs;
		this.data_.description = result.description;
	}
	if (!this.data_.blobs || this.data_.blobs.length == 0) {
		this.resetData_();
	}

	if (result.continue) {
		this.continuation_ = this.getContinuation_(this.constructor.SEARCH_SESSION_CHANGE_TYPE.APPEND, result.continue);
	} else {
		this.continuation_ = null;
	}

	this.dispatchEvent({type: this.constructor.SEARCH_SESSION_CHANGED, changeType: changeType});

	if (changeType == this.constructor.SEARCH_SESSION_CHANGE_TYPE.NEW ||
		changeType == this.constructor.SEARCH_SESSION_CHANGE_TYPE.APPEND) {
		this.startSocketQuery_();
	}
};

cam.SearchSession.prototype.startSocketQuery_ = function() {
	if (!this.socketUri_) {
		return;
	}

	if (this.socket_) {
		this.socket_.close();
	}

	var numResults = 0;
	if (this.data_ && this.data_.blobs) {
		numResults = this.data_.blobs.length;
	}
	var query = this.connection_.buildQuery(this.query_, this.constructor.DESCRIBE_REQUEST, Math.max(numResults, this.constructor.PAGE_SIZE_));

	this.socket_ = new WebSocket(this.socketUri_.toString());
	this.socket_.onopen = function() {
		var message = {
			tag: 'q' + this.instance_,
			query: query
		};
		this.socket_.send(JSON.stringify(message));
	}.bind(this);
	this.socket_.onmessage = function() {
		this.supportsWebSocket_ = true;
		// Ignore the first response.
		this.socket_.onmessage = function(e) {
			var result = JSON.parse(e.data);
			this.searchDone_(this.constructor.SEARCH_SESSION_CHANGE_TYPE.UPDATE, result.result);
		}.bind(this);
	}.bind(this);
};
