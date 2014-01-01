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

goog.provide('SearchSession');

goog.require('goog.events.EventTarget');
goog.require('goog.Uri');
goog.require('goog.Uri.QueryData');
goog.require('goog.uri.utils');

goog.require('camlistore.ServerConnection');

// A search session is a standing query that notifies you when results change. It caches previous results and handles merging new data as it is received. It does not tell you _what_ changed; clients must reconcile as they see fit.
//
// TODO(aa): Only deltas should be sent from server to client
// TODO(aa): Need some way to avoid the duplicate query when websocket starts. Ideas:
// - Initial XHR query can also specify tag. This tag times out if not used rapidly. Send this same tag in socket query.
// - Socket assumes that client already has first batch of results (slightly racey though)
// - Prefer to use socket on client-side, test whether it works and fall back to XHR if not.
var SearchSession = function(connection, currentUri, query) {
	this.connection_ = connection;
	this.initSocketUri_(currentUri);
	this.query_ = query;

	this.data_ = {
		blobs: [],
		description: {}
	};
	this.instance_ = this.constructor.instanceCount_++;
	this.isComplete_ = false;
	this.continuation_ = this.getContinuation_(this.constructor.SEARCH_SESSION_CHANGE_TYPE.NEW);
	this.socket_ = null;
	this.supportsWebSocket_ = false;
};
goog.inherits(SearchSession, goog.events.EventTarget);

// We fire this event when the data changes in any way.
SearchSession.SEARCH_SESSION_CHANGED = 'search-session-change';

// TODO(aa): This should go away once BlobItemContainer can reconcile changes for itself.
SearchSession.SEARCH_SESSION_CHANGE_TYPE = {
	NEW: 1,
	APPEND: 2,
	UPDATE: 3
};

// This size doesn't matter, we don't use it. We only care about the aspect ratio.
// TODO(aa): Change describe to just return aspect directly.
SearchSession.prototype.THUMBNAIL_SIZE_ = 1000;

SearchSession.prototype.PAGE_SIZE_ = 50;

SearchSession.instanceCount_ = 0;

// Returns all the data we currently have loaded.
SearchSession.prototype.getCurrentResults = function() {
	return this.data_;
};

// Loads the next page of data. This is safe to call while a load is in progress; multiple calls for the same page will be collapsed. The SEARCH_SESSION_CHANGED event will be dispatched when the new data is available.
SearchSession.prototype.loadMoreResults = function() {
	if (!this.continuation_) {
		return;
	}

	var c = this.continuation_;
	this.continuation_ = null;
	c();
};

// Returns true if it is known that all data which can be loaded for this query has been.
SearchSession.prototype.isComplete = function() {
	return this.isComplete_;
}

SearchSession.prototype.supportsChangeNotifications = function() {
	return this.supportsWebSocket_;
};

SearchSession.prototype.close = function() {
	if (this.socket_) {
		this.socket.close();
	}
};

SearchSession.prototype.initSocketUri_ = function(currentUri) {
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

SearchSession.prototype.getContinuation_ = function(changeType, opt_continuationToken) {
	var describe = {
		thumbnailSize: this.THUMBNAIL_SIZE_
	};
	return this.connection_.search.bind(this.connection_, this.query_, describe, this.PAGE_SIZE_, opt_continuationToken,
		this.searchDone_.bind(this, changeType));
};

SearchSession.prototype.searchDone_ = function(changeType, result) {
	if (changeType == this.constructor.SEARCH_SESSION_CHANGE_TYPE.APPEND) {
		this.data_.blobs = this.data_.blobs.concat(result.blobs);
		goog.mixin(this.data_.description, result.description);
	} else {
		this.data_.blobs = result.blobs;
		this.data_.description = result.description;
	}

	this.dispatchEvent({type: this.constructor.SEARCH_SESSION_CHANGED, changeType: changeType});

	if (result.continue) {
		this.continuation_ = this.getContinuation_(this.constructor.SEARCH_SESSION_CHANGE_TYPE.APPEND, result.continue);
	} else {
		this.isComplete_ = true;
	}

	if (changeType == this.constructor.SEARCH_SESSION_CHANGE_TYPE.NEW ||
		changeType == this.constructor.SEARCH_SESSION_CHANGE_TYPE.APPEND) {
		this.startSocketQuery_();
	}
};

SearchSession.prototype.startSocketQuery_ = function() {
	if (!this.socketUri_) {
		return;
	}

	if (this.socket_) {
		this.socket_.close();
	}

	var describe = {
		thumbnailSize: this.THUMBNAIL_SIZE_
	};
	var query = this.connection_.buildQuery(this.query_, describe, this.data_.blobs.length);

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
