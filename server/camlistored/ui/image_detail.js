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

cam.ImageDetail = React.createClass({
	displayName: 'ImageDetail',

	IMG_MARGIN: 20,
	PIGGY_WIDTH: 88,
	PIGGY_HEIGHT: 62,

	propTypes: {
		backwardPiggy: false,
		blobItemData: React.PropTypes.instanceOf(cam.BlobItemReactData).isRequired,
		height: React.PropTypes.number.isRequired,
		oldURL: React.PropTypes.instanceOf(goog.Uri).isRequired,
		onEscape: React.PropTypes.func.isRequired,
		searchURL: React.PropTypes.instanceOf(goog.Uri).isRequired,
		width: React.PropTypes.number.isRequired,
	},

	getInitialState: function() {
		this.imgSize_ = null;
		this.lastImageHeight_ = 0;

		return {
			imgHasLoaded: false,
		};
	},

	componentWillReceiveProps: function(nextProps) {
		if (this.props.blobItemData.blobref != nextProps.blobItemData.blobref) {
			this.imgSize_ = null;
			this.lastImageHeight_ = 0;
			this.setState({imgHasLoaded: false});
		}
	},

	componentDidMount: function() {
		this.componentDidUpdate();
	},

	componentDidUpdate: function() {
		var img = this.getImageRef_();
		if (img) {
			// This function gets called multiple times, but the DOM de-dupes listeners for us. Thanks DOM.
			img.getDOMNode().addEventListener('load', this.onImgLoad_);
			img.getDOMNode().addEventListener('error', function() {
				console.error('Could not load image: %s', img.props.src);
			})
		}
	},

	render: function() {
		this.imgSize_ = this.getImgSize_();
		return React.DOM.div({className:'detail-view', style: this.getStyle_()}, [
			this.getImg_(),
			this.getPiggy_(),
			this.getSidebar_(),
		]);
	},

	getSidebar_: function() {
		return cam.PropertySheetContainer({className:'detail-view-sidebar', style:this.getSidebarStyle_()}, [
			this.getGeneralProperties_(),
			this.getFileishProperties_(),
			this.getImageProperties_(),
			this.getNavProperties_(),
		]);
	},

	getGeneralProperties_: function() {
		if (this.props.blobItemData.m.camliType != 'permanode') {
			return null;
		}
		return cam.PropertySheet({key:'general', title:'Generalities'}, [
			React.DOM.h1({className:'detail-title'}, this.getSinglePermanodeAttr_('title') || '<no title>'),
			React.DOM.p({className:'detail-description'}, this.getSinglePermanodeAttr_('description') || '<no description>'),
		]);
	},

	getFileishProperties_: function() {
		var isFile = this.props.blobItemData.rm.camliType == 'file';
		var isDir = this.props.blobItemData.rm.camliType == 'directory';
		if (!isFile && !isDir) {
			return null;
		}
		return cam.PropertySheet({className:'detail-fileish-properties', key:'file', title: isFile ? 'File' : 'Directory'}, [
			React.DOM.table({}, [
				React.DOM.tr({}, [
					React.DOM.td({}, 'filename'),
					React.DOM.td({}, isFile ? this.props.blobItemData.rm.file.fileName : this.props.blobItemData.rm.dir.fileName),
				]),
				React.DOM.tr({}, [
					React.DOM.td({}, 'size'),
					React.DOM.td({}, this.props.blobItemData.rm.file.size + ' bytes'),  // TODO(aa): Humanize units
				]),
			]),
		]);
	},

	getImageProperties_: function() {
		if (!this.props.blobItemData.im) {
			return null;
		}

		return cam.PropertySheet({className:'detail-image-properties', key:'image', title: 'Image'}, [
			React.DOM.table({}, [
				React.DOM.tr({}, [
					React.DOM.td({}, 'width'),
					React.DOM.td({}, this.props.blobItemData.im.width),
				]),
				React.DOM.tr({}, [
					React.DOM.td({}, 'height'),
					React.DOM.td({}, this.props.blobItemData.im.height),
				]),
				// TODO(aa): encoding type, exif data, etc.
			]),
		]);
	},

	getNavProperties_: function() {
		return cam.PropertySheet({key:'nav', title:'Elsewhere'}, [
			React.DOM.a({key:'search-link', href:this.props.searchURL.toString(), onClick:this.props.onEscape}, 'Back to search'),
			React.DOM.br(),
			React.DOM.a({key:'old-link', href:this.props.oldURL.toString()}, 'Old (editable) UI'),
		]);
	},

	// TODO(aa): We need a Permanode utility class that wraps the JSON goop.
	getSinglePermanodeAttr_: function(name) {
		var m = this.props.blobItemData.m;
		if (m.camliType == 'permanode' && m.permanode.attr[name]) {
			return goog.isArray(m.permanode.attr[name]) ? m.permanode.attr[name][0] : m.permanode.attr[name];
		} else {
			return null;
		}
	},

	onImgLoad_: function() {
		this.setState({imgHasLoaded:true});
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
				cam.SpritedAnimation({
					src: 'glitch/npc_piggy__x1_walk_png_1354829432.png',
					className: React.addons.classSet({
						'detail-view-piggy': true,
						'detail-view-piggy-backward': this.props.backwardPiggy
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
		this.lastImageHeight_ = Math.min(this.props.blobItemData.im.height, cam.imageUtil.getSizeToRequest(this.imgSize_.height, this.lastImageHeight_));
		return this.props.blobItemData.getThumbSrc(this.lastImageHeight_);
	},

	getImgSize_: function() {
		if (!this.props.blobItemData || !this.props.blobItemData.im) {
			return null;
		}
		var rawSize = new goog.math.Size(this.props.blobItemData.im.width, this.props.blobItemData.im.height);
		var available = new goog.math.Size(
			this.props.width - this.getSidebarWidth_() - this.IMG_MARGIN * 2,
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

	getSidebarStyle_: function() {
		return {
			width: this.getSidebarWidth_()
		}
	},

	getSidebarWidth_: function() {
		return Math.max(this.props.width * 0.2, 300);
	},

	getPermanodeMeta_: function() {
		if (!this.props.blobItemData) {
			return null;
		}
		return this.props.blobItemData.m;
	},

	getImageRef_: function() {
		return this.refs && this.refs[this.getImageId_()];
	},

	getImageId_: function() {
		return 'img' + this.props.blobItemData.blobref;
	}
});
