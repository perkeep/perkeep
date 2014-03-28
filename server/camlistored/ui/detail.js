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

goog.provide('cam.DetailView');

goog.require('goog.array');
goog.require('goog.events.EventHandler');
goog.require('goog.math.Size');
goog.require('goog.object');
goog.require('goog.string');

goog.require('cam.AnimationLoop');
goog.require('cam.BlobItemReactData');
goog.require('cam.ImageDetail');
goog.require('cam.imageUtil');
goog.require('cam.Navigator');
goog.require('cam.reactUtil');
goog.require('cam.PropertySheetContainer');
goog.require('cam.SearchSession');
goog.require('cam.SpritedAnimation');

cam.DetailView = React.createClass({
	displayName: 'DetailView',

	propTypes: {
		blobref: React.PropTypes.string.isRequired,
		getDetailURL: React.PropTypes.func.isRequired,
		history: cam.reactUtil.quacksLike({go:React.PropTypes.func.isRequired}).isRequired,
		height: React.PropTypes.number.isRequired,
		keyEventTarget: React.PropTypes.object.isRequired, // An event target we will addEventListener() on to receive key events.
		navigator: React.PropTypes.instanceOf(cam.Navigator).isRequired,
		oldURL: React.PropTypes.instanceOf(goog.Uri).isRequired,
		searchSession: React.PropTypes.instanceOf(cam.SearchSession).isRequired,
		searchURL: React.PropTypes.instanceOf(goog.Uri).isRequired,
		width: React.PropTypes.number.isRequired,
	},

	getInitialState: function() {
		return {
			lastNavigateWasBackward: false,
		};
	},

	componentWillMount: function() {
		this.pendingNavigation_ = 0;
		this.navCount_ = 1;
		this.eh_ = new goog.events.EventHandler(this);
	},

	componentDidMount: function(root) {
		this.eh_.listen(this.props.searchSession, cam.SearchSession.SEARCH_SESSION_CHANGED, this.searchUpdated_);
		this.eh_.listen(this.props.keyEventTarget, 'keyup', this.handleKeyUp_);
		this.searchUpdated_();
	},

	render: function() {
		if (!this.dataIsLoaded_()) {
			return React.DOM.div();
		}

		// TODO(aa): Different types of detail views can go here based on what's in blobItemData.
		return cam.ImageDetail({
			backwardPiggy: this.state.lastNavigateWasBackward,
			blobItemData: new cam.BlobItemReactData(this.props.blobref, this.props.searchSession.getCurrentResults().description.meta),
			height: this.props.height,
			oldURL: this.props.oldURL,
			onEscape: this.handleEscape_,
			searchURL: this.props.searchURL,
			width: this.props.width,
		});
	},

	componentWillUnmount: function() {
		this.eh_.dispose();
	},

	handleKeyUp_: function(e) {
		if (e.keyCode == goog.events.KeyCodes.LEFT) {
			this.navigate_(-1);
		} else if (e.keyCode == goog.events.KeyCodes.RIGHT) {
			this.navigate_(1);
		} else if (e.keyCode == goog.events.KeyCodes.ESC) {
			this.handleEscape_(e);
		}
	},

	navigate_: function(offset) {
		this.pendingNavigation_ = offset;
		++this.navCount_;
		this.setState({lastNavigateWasBackward: offset < 0});
		this.handlePendingNavigation_();
	},

	handleEscape_: function(e) {
		e.preventDefault();
		e.stopPropagation();
		history.go(-this.navCount_);
	},

	handlePendingNavigation_: function() {
		if (!this.pendingNavigation_) {
			return;
		}

		var results = this.props.searchSession.getCurrentResults();
		var index = goog.array.findIndex(results.blobs, function(elm) {
			return elm.blob == this.props.blobref;
		}.bind(this));

		if (index == -1) {
			this.props.searchSession.loadMoreResults();
			return;
		}

		index += this.pendingNavigation_;
		if (index < 0) {
			this.pendingNavigation_ = 0;
			console.log('Cannot navigate past beginning of search result.');
			return;
		}

		if (index >= results.blobs.length) {
			if (this.props.searchSession.isComplete()) {
				this.pendingNavigation_ = 0;
				console.log('Cannot navigate past end of search result.');
			} else {
				this.props.searchSession.loadMoreResults();
			}
			return;
		}

		this.props.navigator.navigate(this.props.getDetailURL(results.blobs[index].blob));
	},

	searchUpdated_: function() {
		this.handlePendingNavigation_();

		if (this.dataIsLoaded_()) {
			this.forceUpdate();
			return;
		}

		if (this.props.searchSession.isComplete()) {
			// TODO(aa): 404 UI.
			var error = goog.string.subs('Could not find blobref %s in search session.', this.props.blobref);
			alert(error);
			throw new Error(error);
		}

		// TODO(aa): This can be inefficient in the case of a fresh page load if we have to load lots of pages to find the blobref.
		// Our search protocol needs to be updated to handle the case of paging ahead to a particular item.
		this.props.searchSession.loadMoreResults();
	},

	dataIsLoaded_: function() {
		return Boolean(this.props.searchSession.getCurrentResults().description.meta[this.props.blobref]);
	},
});
