/*
Copyright 2014 The Camlistore Authors

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

goog.provide('cam.ImageDetail');

goog.require('cam.BlobItemVideoContent');
goog.require('cam.Thumber');

// Renders the guts of the detail view for images.
cam.ImageDetail = React.createClass({
	displayName: 'ImageDetail',

	IMG_MARGIN: 20,
	PIGGY_WIDTH: 88,
	PIGGY_HEIGHT: 62,

	propTypes: {
		backwardPiggy: React.PropTypes.bool.isRequired,
		height: React.PropTypes.number.isRequired,
		permanodeMeta: React.PropTypes.object,
		resolvedMeta: React.PropTypes.object.isRequired,
		width: React.PropTypes.number.isRequired,
	},

	isVideo_: function() {
		return !this.isImage_();
	},

	isImage_: function() {
		return Boolean(this.props.resolvedMeta.image);
	},

	componentWillReceiveProps: function(nextProps) {
		if (this.props == nextProps || this.props.resolvedMeta.blobRef != nextProps.resolvedMeta.blobRef) {
			this.thumber_ = nextProps.resolvedMeta.image && cam.Thumber.fromImageMeta(nextProps.resolvedMeta);
			this.setState({imgHasLoaded: false});
		}
	},

	componentWillMount: function() {
		this.componentWillReceiveProps(this.props, true);
	},

	render: function() {
		this.imgSize_ = this.getImgSize_();
		return React.DOM.div({className:'detail-view', style: this.getStyle_()},
			this.getImg_(),
			this.getPiggy_()
		);
	},

	getSinglePermanodeAttr_: function(name) {
		return cam.permanodeUtils.getSingleAttr(this.props.permanodeMeta.permanode, name);
	},

	onImgLoad_: function() {
		this.setState({imgHasLoaded:true});
	},

	getImg_: function() {
		var transition = React.addons.TransitionGroup({transitionName: 'detail-img'}, []);
		if (this.imgSize_) {
			var ctor = this.props.resolvedMeta.image ? React.DOM.img : React.DOM.video;
			transition.props.children.push(
				ctor({
					className: React.addons.classSet({
						'detail-view-img': true,
						'detail-view-img-loaded': this.isImage_() ? this.state.imgHasLoaded : true,
					}),
					controls: true,
					// We want each image to have its own node in the DOM so that during the crossfade, we don't see the image jump to the next image's size.
					key: 'img' + this.props.resolvedMeta.blobRef,
					onLoad: this.isImage_() ? this.onImgLoad_ : null,
					src: this.isImage_() ? this.thumber_.getSrc(this.imgSize_.height) : './download/' + this.props.resolvedMeta.blobRef + '/' + this.props.resolvedMeta.file.fileName,
					style: this.getCenteredProps_(this.imgSize_.width, this.imgSize_.height)
				})
			);
		}
		return transition;
	},

	getPiggy_: function() {
		var transition = React.addons.TransitionGroup({transitionName: 'detail-piggy'}, []);
		if (this.isImage_() && !this.state.imgHasLoaded) {
			transition.props.children.push(
				cam.SpritedAnimation({
					key: 'piggy-sprite',
					src: 'glitch/npc_piggy__x1_walk_png_1354829432.png',
					className: React.addons.classSet({
						'detail-view-piggy': true,
						'detail-view-piggy-backward': this.props.backwardPiggy
					}),
					numFrames: 24,
					spriteWidth: this.PIGGY_WIDTH,
					spriteHeight: this.PIGGY_HEIGHT,
					sheetWidth: 8,
					style: this.getCenteredProps_(this.PIGGY_WIDTH, this.PIGGY_HEIGHT)
				}));
		}
		return transition;
	},

	getCenteredProps_: function(w, h) {
		var avail = new goog.math.Size(this.props.width, this.props.height);
		return {
			top: (avail.height - h) / 2,
			left: (avail.width - w) / 2,
			width: w,
			height: h
		}
	},

	getImgSize_: function() {
		if (this.isVideo_()) {
			return new goog.math.Size(this.props.width, this.props.height);
		}
		var rawSize = new goog.math.Size(this.props.resolvedMeta.image.width, this.props.resolvedMeta.image.height);
		var available = new goog.math.Size(
			this.props.width - this.IMG_MARGIN * 2,
			this.props.height - this.IMG_MARGIN * 2);
		if (rawSize.height <= available.height && rawSize.width <= available.width) {
			return rawSize;
		}
		return rawSize.scaleToFit(available);
	},

	getStyle_: function() {
		return {
			width: this.props.width,
			height: this.props.height
		}
	},
});

cam.ImageDetail.getAspect = function(blobref, searchSession) {
	if (!blobref) {
		return null;
	}

	var rm = searchSession.getResolvedMeta(blobref);
	var pm = searchSession.getMeta(blobref);

	if (!pm) {
		return null;
	}

	if (pm.camliType != 'permanode') {
		pm = null;
	}

	// We don't handle camliContentImage like BlobItemImage.getHandler does because that only tells us what image to display in the search results. It doesn't actually make the permanode an image or anything.
	if (rm && (rm.image || cam.BlobItemVideoContent.isVideo(rm))) {
		return {
			fragment: 'image',
			title: 'Image',
			createContent: function(size, backwardPiggy) {
				return cam.ImageDetail({
					backwardPiggy: backwardPiggy,
					key: 'image',
					height: size.height,
					permanodeMeta: pm,
					resolvedMeta: rm,
					width: size.width,
				});
			},
		};
	} else {
		return null;
	}
};
