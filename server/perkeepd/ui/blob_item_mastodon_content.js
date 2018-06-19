/*
Copyright 2018 The Perkeep Authors

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

goog.provide('cam.BlobItemMastodonContent');

goog.require('goog.math.Size');

goog.require('cam.Thumber');
goog.require('cam.permanodeUtils');

cam.BlobItemMastodonContent = React.createClass({
	propTypes: {
		date: React.PropTypes.number.isRequired,
		size: React.PropTypes.instanceOf(goog.math.Size).isRequired,
		href: React.PropTypes.string.isRequired,
		image: React.PropTypes.string,
		spoiler: React.PropTypes.string,
	},

	render: function() {

		var className = 'cam-blobitem-mastodon-status'
		var style = {
			width: this.props.size.width,
			height: this.props.size.height,
		}

		if (this.props.image) {
			style['backgroundImage'] = 'linear-gradient(#e1e8ed64,#e1e8ed64),'+
				'url(' + this.props.image + ')';
			className += " cam-blobitem-mastodon-status-image"
		}

		return React.DOM.a({
				href: this.props.href,
				className: className,
				style: style,
			},
			React.DOM.div({className: 'cam-blobitem-mastodon-date'}, cam.dateUtils.formatDateShort(this.props.date)),
			React.DOM.div({
					className: 'cam-blobitem-mastodon-spoiler',
					dangerouslySetInnerHTML: {__html: this.props.spoiler }
				}
			),
			React.DOM.div({
					className: 'cam-blobitem-mastodon-content',
					dangerouslySetInnerHTML: {__html: this.props.content }
				}
			)
		);
	},

})



cam.BlobItemMastodonContent.Handler = function(content, date, href, imageMeta, spoiler) {
	this.content_ = content;
	this.date_ = date;
	this.href_ = href;
	this.thumber_ = imageMeta ? cam.Thumber.fromImageMeta(imageMeta) : null;
	this.spoiler_ = spoiler;

};

cam.BlobItemMastodonContent.getHandler = function(blobref, searchSession, href) {
	var meta = searchSession.getMeta(blobref);
	if ((meta.camliType != 'permanode') ||
		(cam.permanodeUtils.getCamliNodeType(meta.permanode) != 'mastodon:status')) {
		return null;
	}

	var date = Date.parse(cam.permanodeUtils.getSingleAttr(meta.permanode, 'startDate'));
	var content = cam.permanodeUtils.getSingleAttr(meta.permanode, 'content');

	if (!date || !content) {
		return null;
	}

	var spoiler = cam.permanodeUtils.getSingleAttr(meta.permanode, 'spoilerText');

	var imageAttr = cam.permanodeUtils.getSingleAttr(meta.permanode, 'camliContentImage');
	var image;
	if (imageAttr) {
		image = searchSession.getResolvedMeta(imageAttr);
	}

	return new cam.BlobItemMastodonContent.Handler(content, date, href, image, spoiler);
}

cam.BlobItemMastodonContent.Handler.prototype.getAspectRatio = function() {
	return 1.0;
};

cam.BlobItemMastodonContent.Handler.prototype.createContent = function(size) {
	return React.createElement(cam.BlobItemMastodonContent, {
		content: this.content_,
		date: this.date_,
		size: size,
		href: this.href_,
		image: this.thumber_ ? this.thumber_.getSrc(size) : null,
		spoiler: this.spoiler_,
	});
};