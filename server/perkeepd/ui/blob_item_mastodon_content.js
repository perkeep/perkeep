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
goog.require('goog.object');

goog.require('cam.dateUtils');
goog.require('cam.permanodeUtils');
goog.require('cam.Thumber');

// BlobItemMastodonContent shows a permanode of the Mastodon importer: a
// status, the account's own or another's; a boost, favourite or bookmark,
// with the status it's of; or a follow or profile, with the profile's
// picture and name.
cam.BlobItemMastodonContent = React.createClass({
	propTypes: {
		href: React.PropTypes.string.isRequired,
		size: React.PropTypes.instanceOf(goog.math.Size).isRequired,
		kind: React.PropTypes.string.isRequired, // a key of cam.BlobItemMastodonContent.KINDS
		date: React.PropTypes.number,
		author: React.PropTypes.string, // user@domain of another's status, or of a profile
		name: React.PropTypes.string, // of a profile
		visibility: React.PropTypes.string, // of a status, if not public
		spoiler: React.PropTypes.string, // a status's content warning
		text: React.PropTypes.string,
		linkTitle: React.PropTypes.string,
		reply: React.PropTypes.bool,
		media: React.PropTypes.string, // a badge of a status's video or audio, if any
		image: React.PropTypes.string, // of a status, or a profile's picture
		missing: React.PropTypes.string, // why the status or profile isn't shown, if it isn't
	},

	render: function() {
		var kind = cam.BlobItemMastodonContent.KINDS[this.props.kind];
		var isProfile = this.props.kind == 'follow' || this.props.kind == 'profile' || this.props.kind == 'linkedProfile';
		return React.DOM.a({
				href: this.props.href,
				className: 'cam-blobitem-masto cam-blobitem-masto-' + this.props.kind,
				style: {
					width: this.props.size.width,
					height: this.props.size.height,
				},
			},
			React.DOM.div({className: 'cam-blobitem-masto-head'},
				kind.action ? React.DOM.span({className: 'cam-blobitem-masto-action'}, kind.action) : null,
				this.props.date ? React.DOM.span({className: 'cam-blobitem-masto-date'}, cam.dateUtils.formatDateShort(this.props.date)) : null,
				this.props.visibility ? React.DOM.span({className: 'cam-blobitem-masto-visibility'},
					cam.BlobItemMastodonContent.VISIBILITIES[this.props.visibility] || this.props.visibility) : null
			),
			isProfile ? this.getProfile_() : this.getStatus_(),
			React.DOM.div({className: 'cam-blobitem-masto-mark'}, 'Mastodon')
		);
	},

	getStatus_: function() {
		if (this.props.missing) {
			return React.DOM.div({className: 'cam-blobitem-masto-missing'}, this.props.missing);
		}
		return [
			this.props.author ? React.DOM.div({key: 'author', className: 'cam-blobitem-masto-author'}, '@' + this.props.author) : null,
			this.props.reply ? React.DOM.div({key: 'reply', className: 'cam-blobitem-masto-reply'}, 'Reply') : null,
			this.props.spoiler ? React.DOM.div({key: 'spoiler', className: 'cam-blobitem-masto-spoiler'}, this.props.spoiler) : null,
			this.props.text ? React.DOM.div({key: 'text', className: 'cam-blobitem-masto-text'}, this.props.text) : null,
			this.props.linkTitle ? React.DOM.div({key: 'link', className: 'cam-blobitem-masto-link'}, this.props.linkTitle) : null,
			this.props.image ? React.DOM.div({
					key: 'image',
					className: 'cam-blobitem-masto-image',
					style: {backgroundImage: 'url(' + this.props.image + ')'},
				},
				this.props.media ? React.DOM.span({className: 'cam-blobitem-masto-media'}, this.props.media) : null
			) : (this.props.media ? React.DOM.div({key: 'media', className: 'cam-blobitem-masto-media'}, this.props.media) : null),
		];
	},

	getProfile_: function() {
		if (this.props.missing) {
			return React.DOM.div({className: 'cam-blobitem-masto-missing'}, this.props.missing);
		}
		return React.DOM.div({className: 'cam-blobitem-masto-profile'},
			this.props.image ? React.DOM.div({
				className: 'cam-blobitem-masto-avatar',
				style: {backgroundImage: 'url(' + this.props.image + ')'},
			}) : null,
			this.props.name ? React.DOM.div({className: 'cam-blobitem-masto-name'}, this.props.name) : null,
			this.props.author ? React.DOM.div({className: 'cam-blobitem-masto-author'}, '@' + this.props.author) : null
		);
	},
});

// The camliNodeType values of the importer's permanodes of statuses and
// profiles.
cam.BlobItemMastodonContent.STATUS = 'mastodon:status'; // the account's own
cam.BlobItemMastodonContent.LINKED_STATUS = 'mastodon:linkedStatus'; // another's
cam.BlobItemMastodonContent.PROFILE = 'mastodon:profile'; // the account's own
cam.BlobItemMastodonContent.LINKED_PROFILE = 'mastodon:linkedProfile'; // of an account it follows

// KINDS are the kinds of permanodes shown, by name: their camliNodeType
// values, and, for those of statuses or profiles, the action they show
// above them.
cam.BlobItemMastodonContent.KINDS = {
	status: {type: cam.BlobItemMastodonContent.STATUS},
	linkedStatus: {type: cam.BlobItemMastodonContent.LINKED_STATUS},
	reblog: {type: 'mastodon:reblog', action: '↻ Boosted'},
	favourite: {type: 'mastodon:favourite', action: '★ Favourited'},
	bookmark: {type: 'mastodon:bookmark', action: 'Bookmarked'},
	follow: {type: 'mastodon:follow', action: 'Followed'},
	profile: {type: cam.BlobItemMastodonContent.PROFILE},
	linkedProfile: {type: cam.BlobItemMastodonContent.LINKED_PROFILE},
};

// VISIBILITIES are the labels of statuses' visibilities other than public.
cam.BlobItemMastodonContent.VISIBILITIES = {
	unlisted: 'Unlisted',
	private: 'Followers only',
	direct: 'Direct message',
};

// kindOf returns the name of the kind of permanode of the camliNodeType
// typ, or the empty string if it's not one shown.
cam.BlobItemMastodonContent.kindOf = function(typ) {
	for (var k in cam.BlobItemMastodonContent.KINDS) {
		if (cam.BlobItemMastodonContent.KINDS[k].type == typ) {
			return k;
		}
	}
	return '';
};

// mediaBadge returns the badge of the media of the status of permanode
// pn: of its video, if it has one, or else its audio, or the empty string.
cam.BlobItemMastodonContent.mediaBadge = function(pn) {
	var video = false, audio = false;
	for (var k in pn.attr) {
		if (k.indexOf('camliPath:video') == 0 || k.indexOf('camliPath:gifv') == 0) {
			video = true;
		} else if (k.indexOf('camliPath:audio') == 0) {
			audio = true;
		}
	}
	return video ? '▶' : (audio ? '♪' : '');
};

cam.BlobItemMastodonContent.getHandler = function(blobref, searchSession, href) {
	var m = searchSession.getMeta(blobref);
	if (m.camliType != 'permanode') {
		return null;
	}
	var kind = cam.BlobItemMastodonContent.kindOf(cam.permanodeUtils.getCamliNodeType(m.permanode));
	if (!kind) {
		return null;
	}
	var props = {kind: kind};
	var date = Date.parse(cam.permanodeUtils.getSingleAttr(m.permanode, 'startDate'));
	if (date) {
		props.date = date;
	}
	// What a boost, favourite, bookmark or follow is of: the status or
	// profile that is its content.
	var subject = m.permanode;
	var isRef = kind == 'reblog' || kind == 'favourite' || kind == 'bookmark' || kind == 'follow';
	if (isRef) {
		var want = kind == 'follow' ? [cam.BlobItemMastodonContent.LINKED_PROFILE] :
			[cam.BlobItemMastodonContent.LINKED_STATUS, cam.BlobItemMastodonContent.STATUS];
		var cm = searchSession.getResolvedMeta(blobref);
		subject = null;
		if (cm && cm.camliType == 'permanode' && want.indexOf(cam.permanodeUtils.getCamliNodeType(cm.permanode)) >= 0) {
			subject = cm.permanode;
		}
	}
	if (!subject) {
		props.missing = 'The ' + (kind == 'follow' ? 'profile' : 'status') + ' wasn’t found.';
		return new cam.BlobItemMastodonContent.Handler(href, props, null);
	}
	var attr = function(name) {
		return cam.permanodeUtils.getSingleAttr(subject, name) || '';
	};
	if (kind == 'follow' || kind == 'profile' || kind == 'linkedProfile') {
		props.author = attr('mastodonAccount');
		// Its title is its name and then its account's.
		props.name = attr('title').replace(' (@' + props.author + ')', '');
	} else {
		// Favourites and bookmarks are undated, unlike boosts: show
		// their statuses' dates.
		var statusDate = Date.parse(attr('startDate'));
		if (!props.date && statusDate) {
			props.date = statusDate;
		}
		props.author = attr('mastodonAuthor');
		var visibility = attr('mastodonVisibility');
		if (visibility && visibility != 'public') {
			props.visibility = visibility;
		}
		props.spoiler = attr('spoilerText');
		props.text = attr('content');
		props.linkTitle = attr('linkTitle');
		props.reply = !!(attr('inReplyTo') || attr('mastodonInReplyToId'));
		props.media = cam.BlobItemMastodonContent.mediaBadge(subject);
	}
	var imageRef = attr('camliContentImage');
	var imageMeta = imageRef ? searchSession.getResolvedMeta(imageRef) : null;
	return new cam.BlobItemMastodonContent.Handler(href, props, imageMeta ? cam.Thumber.fromImageMeta(imageMeta) : null);
};

cam.BlobItemMastodonContent.Handler = function(href, props, thumber) {
	this.href_ = href;
	this.props_ = props;
	this.thumber_ = thumber;
};

cam.BlobItemMastodonContent.Handler.prototype.getAspectRatio = function() {
	return 1.0;
};

cam.BlobItemMastodonContent.Handler.prototype.createContent = function(size) {
	var props = goog.object.clone(this.props_);
	props.href = this.href_;
	props.size = size;
	if (this.thumber_) {
		props.image = this.thumber_.getSrc(size);
	}
	return React.createElement(cam.BlobItemMastodonContent, props);
};
