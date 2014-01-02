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

goog.require('camlistore.AnimationLoop');
goog.require('camlistore.ServerConnection');
goog.require('SpritedAnimation');

goog.require('goog.math.Size');
goog.require('goog.object');

var DetailView = React.createClass({
	IMG_MARGIN: 20,
	PIGGY_WIDTH: 88,
	PIGGY_HEIGHT: 62,

	getInitialState: function() {
		this.imgSize_ = null;
		this.lastImageHeight_ = 0;

		return {
			description: null,
			imgHasLoaded: false
		};
	},

	componentDidMount: function(root) {
		var imageSize = 100;  // We won't use this exact value; we only care about the aspect ratio.
		var connection = new camlistore.ServerConnection(this.props.config);
		connection.describeWithThumbnails(this.props.blobref, imageSize, function(description) {
 			this.setState({
				description: description
			});
		}.bind(this));
	},

	componentDidUpdate: function(prevProps, prevState) {
		if (this.refs.img) {
			this.refs.img.getDOMNode().addEventListener('load', this.setState.bind(this, {imgHasLoaded:true}, null));
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
					React.DOM.pre({key:'sidebar-pre'}, JSON.stringify(this.state.description || '', null, 2)))));
	},

	getImg_: function() {
		if (this.state.description) {
			this.img_ = React.DOM.img({
				className: React.addons.classSet({
					'detail-view-img': true,
					'detail-view-img-loaded': this.state.imgHasLoaded
				}),
				key: 'img',
				ref: 'img',
				src: this.getSrc_(),
				style: this.getCenteredProps_(this.imgSize_.width, this.imgSize_.height)
			});
		}
		return this.img_;
	},

	getPiggy_: function() {
		var transition = React.addons.TransitionGroup({transitionName: 'detail-piggy'}, []);
		if (!this.state.imgHasLoaded) {
			transition.props.children.push(
				SpritedAnimation({
					src: 'glitch/npc_piggy__x1_walk_png_1354829432.png',
					className: 'detail-view-piggy',
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
		// Only re-request the image if we're increasing in size. Otherwise, let the browser resample.
		if (this.imgSize_.height < this.lastImageHeight_) {
			console.log('Not re-requesting image becasue new size is smaller than existing...');
		} else {
			// If we re-request, ask for one that is the next biggest power of 2 to avoid lots of requests as we resize, and to increase cache hit rate across sessions.
			var maxImageSize = 2000;  // max size server will accept
			for (var size = 64; (size <= this.imgSize_.height && size < maxImageSize); size <<= 1) {
			}
			this.lastImageHeight_ = Math.min(size, maxImageSize);
			console.log('Requesting new image with size: ' + this.lastImageHeight_);
		}

		var uri = new goog.Uri(this.getPermanodeMeta_().thumbnailSrc);
		uri.setParameterValue('mh', this.lastImageHeight_);
		return uri.toString();
	},

	getImgSize_: function() {
		if (!this.state.description) {
			return null;
		}

		var meta = this.getPermanodeMeta_();
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
		return this.state.description.meta[this.props.blobref];
	},
});
