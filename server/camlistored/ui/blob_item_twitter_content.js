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

goog.provide('cam.BlobItemTwitterContent');

goog.require('goog.math.Size');

goog.require('cam.dateUtils');
goog.require('cam.math');
goog.require('cam.permanodeUtils');
goog.require('cam.Thumber');

cam.BlobItemTwitterContent = React.createClass({
	propTypes: {
		date: React.PropTypes.number.isRequired,
		href: React.PropTypes.string.isRequired,
		image: React.PropTypes.string,
		size: React.PropTypes.instanceOf(goog.math.Size).isRequired,
		username: React.PropTypes.string.isRequired,
	},

	getImageRow_: function() {
		if (!this.props.image) {
			return null;
		}

		return React.DOM.tr(null,
			React.DOM.td({
				className: 'cam-blobitem-twitter-tweet-image',
				colSpan: 2,
				src: 'twitter-icon.png',
				style: {
					backgroundImage: 'url(' + this.props.image + ')',
				},
			})
		);
	},

	render: function() {
		return React.DOM.a({
				href: this.props.href,
				className: 'cam-blobitem-twitter-tweet',
				style: {
					width: this.props.size.width,
					height: this.props.size.height,
				},
			},
			React.DOM.table({height: this.props.image ? '100%' : ''},
				React.DOM.tr(null,
					React.DOM.td({className: 'cam-blobitem-twitter-tweet-meta'},
						React.DOM.span({className: 'cam-blobitem-twitter-tweet-date'}, cam.dateUtils.formatDateShort(this.props.date)),
						React.DOM.br(),
						React.DOM.span({className: ' cam-blobitem-twitter-tweet-content'}, this.props.content)
					)
				),
				this.getImageRow_(),
				React.DOM.tr(null,
					React.DOM.td({className: 'cam-blobitem-twitter-tweet-icon'},
						React.DOM.img({src: 'twitter-logo.png'})
					)
				)
			)
		);
	},
});

cam.BlobItemTwitterContent.getHandler = function(blobref, searchSession, href) {
	var m = searchSession.getMeta(blobref);
	if (m.camliType != 'permanode') {
		return null;
	}

	if (cam.permanodeUtils.getCamliNodeType(m.permanode) != 'twitter.com:tweet') {
		return null;
	}

	var date = cam.permanodeUtils.getSingleAttr(m.permanode, 'startDate');
	var username = cam.permanodeUtils.getSingleAttr(m.permanode, 'url');
	if (!date || !username) {
		return null;
	}

	username = username.match(/^https:\/\/twitter.com\/(.+?)\//)[1];

	// It's OK to not have any content. Tweets can be just images or whatever.
	var content = cam.permanodeUtils.getSingleAttr(m.permanode, 'content');
	var imageMeta = cam.permanodeUtils.getSingleAttr(m.permanode, 'camliContentImage');
	if (imageMeta) {
		imageMeta = searchSession.getResolvedMeta(imageMeta);
	}

	return new cam.BlobItemTwitterContent.Handler(content, Date.parse(date), href, imageMeta, username);
};

cam.BlobItemTwitterContent.Handler = function(content, date, href, imageMeta, username) {
	this.content_ = content;
	this.date_ = date;
	this.href_ = href;
	this.username_ = username;
	this.thumber_ = imageMeta ? new cam.Thumber.fromImageMeta(imageMeta) : null;
};

cam.BlobItemTwitterContent.Handler.prototype.getAspectRatio = function() {
	return 1.0;
};

cam.BlobItemTwitterContent.Handler.prototype.createContent = function(size) {
	return cam.BlobItemTwitterContent({
		content: this.content_,
		date: this.date_,
		href: this.href_,
		image: this.thumber_ ? this.thumber_.getSrc(size) : null,
		size: size,
		username: this.username_,
	});
};
