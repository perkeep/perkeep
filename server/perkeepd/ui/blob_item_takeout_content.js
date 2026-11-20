/*
Copyright 2020 The Perkeep Authors

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

goog.provide('cam.BlobItemTakeoutContent');

goog.require('goog.math.Size');

goog.require('cam.dateUtils');
goog.require('cam.math');
goog.require('cam.permanodeUtils');
goog.require('cam.Thumber');

cam.BlobItemTakeoutContent = React.createClass({
	propTypes: {
		date: React.PropTypes.number.isRequired,
		href: React.PropTypes.string.isRequired,
		image: React.PropTypes.string,
		size: React.PropTypes.instanceOf(goog.math.Size).isRequired,
	},

	render: function() {
		return React.DOM.a({
				href: this.props.href,
				className: 'cam-blobitem-takeout-item',
				style: {
					width: this.props.size.width,
					height: this.props.size.height,
				},
			},
			React.DOM.table({height: this.props.image ? '100%' : ''},
				React.DOM.tbody(null,
					React.DOM.tr(null,
						React.DOM.td({className: 'cam-blobitem-takeout-item-meta'},
							React.DOM.span({className: 'cam-blobitem-takeout-item-date'}, cam.dateUtils.formatDateShort(this.props.date)),
							React.DOM.br(),
							React.DOM.span({className: ' cam-blobitem-takeout-item-title'}, this.props.title),
							React.DOM.br(),
							React.DOM.span({className: ' cam-blobitem-takeout-item-content'}, this.props.content)
						)
					)
				)
			)
		);
	},
});

cam.BlobItemTakeoutContent.getHandler = function(blobref, searchSession, href) {
	var m = searchSession.getMeta(blobref);
	if (m.camliType != 'permanode') {
		return null;
	}

	if (cam.permanodeUtils.getCamliNodeType(m.permanode) != 'google.com:takeout') {
		return null;
	}

	var date = cam.permanodeUtils.getSingleAttr(m.permanode, 'startDate');

	var title = cam.permanodeUtils.getSingleAttr(m.permanode, 'title');
	var content = cam.permanodeUtils.getSingleAttr(m.permanode, 'content');
	var imageMetaBr = cam.permanodeUtils.getSingleAttr(m.permanode, 'camliContentImage');
	var imageMeta = null;
	if (imageMetaBr) {
		imageMeta = searchSession.getResolvedMeta(imageMetaBr);
	}

	return new cam.BlobItemTakeoutContent.Handler(title, content, Date.parse(date), href, imageMeta);
};

cam.BlobItemTakeoutContent.Handler = function(title, content, date, href, imageMeta) {
	this.title_ = title
	this.content_ = content;
	this.date_ = date;
	this.href_ = href;
	this.thumber_ = imageMeta ? cam.Thumber.fromImageMeta(imageMeta) : null;
};

cam.BlobItemTakeoutContent.Handler.prototype.getAspectRatio = function() {
	return 1.0;
};

cam.BlobItemTakeoutContent.Handler.prototype.createContent = function(size) {
	return React.createElement(cam.BlobItemTakeoutContent, {
		title: this.title_,
		content: this.content_,
		date: this.date_,
		href: this.href_,
		image: this.thumber_ ? this.thumber_.getSrc(size) : null,
		size: size,
	});
};
