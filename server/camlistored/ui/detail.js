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
goog.provide('DetailView');

goog.require('camlistore.AnimationLoop');
goog.require('SearchSession');
goog.require('SpritedAnimation');
goog.require('image_utils');

goog.require('goog.array');
goog.require('goog.events.EventHandler');
goog.require('goog.math.Size');
goog.require('goog.object');
goog.require('goog.string');

var DetailView = React.createClass({
	IMG_MARGIN: 20,
	PIGGY_WIDTH: 88,
	PIGGY_HEIGHT: 62,

	getInitialState: function() {
		this.imgSize_ = null;
		this.lastImageHeight_ = 0;
		this.pendingNavigation_ = 0;
		this.eh_ = new goog.events.EventHandler(this);

		return {
			imgHasLoaded: false,
			backwardPiggy: false,
		};
	},

	componentWillReceiveProps: function(nextProps) {
		if (this.props.blobref != nextProps.blobref) {
			this.imgSize_ = null;
			this.lastImageHeight_ = 0;
			this.setState({imgHasLoaded: false});
		}
	},

	componentDidMount: function(root) {
		this.eh_.listen(this.props.searchSession, SearchSession.SEARCH_SESSION_CHANGED, this.searchUpdated_);
		this.searchUpdated_();
	},

	componentDidUpdate: function(prevProps, prevState) {
		var img = this.getImageRef_();
		if (img) {
			// This function gets called multiple times, but the DOM de-dupes listeners for us. Thanks DOM.
			img.getDOMNode().addEventListener('load', this.onImgLoad_);
		}
	},

	render: function() {
		this.imgSize_ = this.getImgSize_();
		return (
			React.DOM.div({className:'detail-view', style: this.getStyle_()},
				this.getImg_(),
				this.getPiggy_(),
				React.DOM.div({className:'detail-view-sidebar', key:'sidebar', style: this.getSidebarStyle_()},
					React.DOM.a({key:'sidebar-link', href:'../ui/?p=' + this.props.blobref}, 'old and busted'),
					React.DOM.pre({key:'sidebar-pre'}, JSON.stringify(this.getPermanodeMeta_(), null, 2)))));
	},

	componentWillUnmount: function() {
		this.eh_.unlisten(this.props.searchSession, SearchSession.SEARCH_SESSION_CHANGED, this.searchUpdated_);
	},

	navigate: function(offset) {
		this.pendingNavigation_ = offset;
		this.setState({backwardPiggy: offset < 0});
		this.handlePendingNavigation_();
	},

	handlePendingNavigation_: function() {
		if (!this.handlePendingNavigation_) {
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

		this.props.onNavigate(results.blobs[index].blob);
	},

	onImgLoad_: function() {
		this.setState({imgHasLoaded:true});
	},

	searchUpdated_: function() {
		this.handlePendingNavigation_();

		if (this.getPermanodeMeta_()) {
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

	getImg_: function() {
		var transition = React.addons.TransitionGroup({transitionName: 'detail-img'}, []);
		if (this.imgSize_) {
			transition.props.children.push(
				React.DOM.img({
					className: React.addons.classSet({
						'detail-view-img': true,
						'detail-view-img-loaded': this.state.imgHasLoaded
					}),
					// We want each image to have its own node in the DOM so that during the crossfade, we don't see the image jump to the next image's size.
					key: this.getImageId_(),
					ref: this.getImageId_(),
					src: this.getSrc_(),
					style: this.getCenteredProps_(this.imgSize_.width, this.imgSize_.height)
				})
			);
		}
		return transition;
	},

	getPiggy_: function() {
		var transition = React.addons.TransitionGroup({transitionName: 'detail-piggy'}, []);
		if (!this.state.imgHasLoaded) {
			transition.props.children.push(
				SpritedAnimation({
					src: 'glitch/npc_piggy__x1_walk_png_1354829432.png',
					className: React.addons.classSet({
						'detail-view-piggy': true,
						'detail-view-piggy-backward': this.state.backwardPiggy
					}),
					spriteWidth: this.PIGGY_WIDTH,
					spriteHeight: this.PIGGY_HEIGHT,
					sheetWidth: 8,
					sheetHeight: 3,
					interval: 30,
					style: this.getCenteredProps_(this.PIGGY_WIDTH, this.PIGGY_HEIGHT)
				}));
		}
		return transition;
	},

	getCenteredProps_: function(w, h) {
		var avail = new goog.math.Size(this.props.width - this.getSidebarWidth_(), this.props.height);
		return {
			top: (avail.height - h) / 2,
			left: (avail.width - w) / 2,
			width: w,
			height: h
		}
	},

	getSrc_: function() {
		this.lastImageHeight_ = image_utils.getSizeToRequest(this.imgSize_.height, this.lastImageHeight_);
		var uri = new goog.Uri(this.getPermanodeMeta_().thumbnailSrc);
		uri.setParameterValue('mh', this.lastImageHeight_);
		return uri.toString();
	},

	getImgSize_: function() {
		var meta = this.getPermanodeMeta_();
		if (!meta) {
			return null;
		}
		var aspect = new goog.math.Size(meta.thumbnailWidth, meta.thumbnailHeight);
		var available = new goog.math.Size(
			this.props.width - this.getSidebarWidth_() - this.IMG_MARGIN * 2,
			this.props.height - this.IMG_MARGIN * 2);
		return aspect.scaleToFit(available);
	},

	getStyle_: function() {
		return {
			width: this.props.width,
			height: this.props.height
		}
	},

	getSidebarStyle_: function() {
		return {
			width: this.getSidebarWidth_()
		}
	},

	getSidebarWidth_: function() {
		return Math.max(this.props.width * 0.2, 300);
	},

	getPermanodeMeta_: function() {
		return this.props.searchSession.getCurrentResults().description.meta[this.props.blobref];
	},

	getImageRef_: function() {
		return this.refs[this.getImageId_()];
	},

	getImageId_: function() {
		return 'img' + this.props.blobref;
	}
});
