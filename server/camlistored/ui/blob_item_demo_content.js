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

goog.provide('cam.BlobItemDemoContent');

goog.require('goog.math.Size');

// BlobItemDemoContent is a demo node type, useful for giving talks and showing
// how a custom renderer can be invoked just by making a permanode, setting
// its "camliNodeType" attribute to "camlistore.org:demo", and then changing its
// background color with the "color" property or text with the "title" property.
cam.BlobItemDemoContent = React.createClass({
	displayName: 'BlobItemDemoContent',

	propTypes: {
		blobref: React.PropTypes.string.isRequired,
		href: React.PropTypes.string.isRequired,
		title: React.PropTypes.string.isRequired,
		size: React.PropTypes.instanceOf(goog.math.Size).isRequired,
		color: React.PropTypes.string.isRequired
	},

	getInitialState: function () {
		return {
			mouseover: false,
		};
	},

	render: function () {
		return React.DOM.a({
				href: this.props.href,
				style: {
					backgroundColor: this.props.color,
					width: this.props.size.width + "px",
					height: this.props.size.height + "px",
					display: 'block'
				},
				onMouseEnter: this.handleMouseOver_,
				onMouseLeave: this.handleMouseOut_
			},
			this.props.title + (this.state.mouseover ? ', mouseover' : '')
		);
	},

	handleMouseOver_: function () {
		this.setState({
			mouseover: true
		});
	},

	handleMouseOut_: function () {
		this.setState({
			mouseover: false
		});
	},
});

cam.BlobItemDemoContent.getHandler = function (blobref, searchSession, href) {
	var m = searchSession.getMeta(blobref);
	if (m.camliType == 'permanode') {
		var typ = cam.permanodeUtils.getCamliNodeType(m.permanode);
		if (typ == 'camlistore.org:demo') {
			return new cam.BlobItemDemoContent.Handler(m, href)
		}
	}
	return null;
};

cam.BlobItemDemoContent.Handler = function (meta, href) {
	this.meta_ = meta;
	this.href_ = href;
};

cam.BlobItemDemoContent.Handler.prototype.getAspectRatio = function () {
	return 1;
};

cam.BlobItemDemoContent.Handler.prototype.createContent = function (size) {
	return cam.BlobItemDemoContent({
		blobref: this.meta_.blobRef,
		color: cam.permanodeUtils.getSingleAttr(this.meta_.permanode, 'color') || '#777',
		title: cam.permanodeUtils.getSingleAttr(this.meta_.permanode, 'title') || '',
		href: this.href_,
		size: size,
	});
};