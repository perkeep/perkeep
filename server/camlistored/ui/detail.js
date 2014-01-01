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
		return {
			description: null,
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

	render: function() {
		this.imgSize_ = this.getImgSize_();
		return (
			React.DOM.div({className:'detail-view', style: this.getStyle_()},
				this.getImg_(),
				this.getPiggy_(),
				React.DOM.div({className:'detail-view-sidebar', key:'sidebar', style: this.getSidebarStyle_()},
					React.DOM.pre({key:'sidebar-pre'}, JSON.stringify(this.state.description || '', null, 2)))));
	},

	getImg_: function() {
		var transition = React.addons.TransitionGroup({transitionName: 'detail-img'}, []);
		if (this.state.description) {
			transition.props.children.push(
				React.DOM.img({
					className: 'detail-view-img',
					key: 'img',
					src: this.getSrc_(),
					style: this.getCenteredProps_(this.imgSize_.width, this.imgSize_.height)
				})
			);
		}
		return transition;
	},

	getPiggy_: function() {
		var transition = React.addons.TransitionGroup({transitionName: 'detail-piggy'}, []);
		if (!this.state.description) {
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
		if (this.imgSize_.height < (this.lastImageHeight || 0)) {
			console.log('Not re-requesting image becasue new size is smaller than existing...');
		} else {
			// If we re-request, ask for one bigger than we need right now, so that we're not constantly re-requesting as the browser resizes.
			this.lastImageHeight = this.imgSize_.height * 1.25;
			console.log('Requesting new image with size: ' + this.lastImageHeight);
		}

		var uri = new goog.Uri(this.getPermanodeMeta_().thumbnailSrc);
		uri.setParameterValue('mh', this.lastImageHeight);
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
