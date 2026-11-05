/*
Copyright 2026 The Perkeep Authors

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

goog.provide('cam.BlobItemBlueskyContent');

goog.require('goog.math.Size');
goog.require('goog.object');

goog.require('cam.dateUtils');
goog.require('cam.permanodeUtils');
goog.require('cam.Thumber');

// BlobItemBlueskyContent shows a permanode of the Bluesky importer: a post;
// a like or repost, with the post it's of, if the importer fetched it; or a
// follow or profile, with the profile's picture and name.
cam.BlobItemBlueskyContent = React.createClass({
	propTypes: {
		href: React.PropTypes.string.isRequired,
		size: React.PropTypes.instanceOf(goog.math.Size).isRequired,
		kind: React.PropTypes.string.isRequired, // a key of cam.BlobItemBlueskyContent.KINDS
		date: React.PropTypes.number,
		author: React.PropTypes.string, // of a post, or the handle of a profile
		name: React.PropTypes.string, // of a profile
		text: React.PropTypes.string,
		linkTitle: React.PropTypes.string,
		reply: React.PropTypes.bool,
		image: React.PropTypes.string, // of a post, or a profile's picture
		missing: React.PropTypes.string, // why the post or profile isn't shown, if it isn't
	},

	render: function() {
		var kind = cam.BlobItemBlueskyContent.KINDS[this.props.kind];
		var isProfile = this.props.kind == 'follow' || this.props.kind == 'profile';
		return React.DOM.a({
				href: this.props.href,
				className: 'cam-blobitem-bsky cam-blobitem-bsky-' + this.props.kind,
				style: {
					width: this.props.size.width,
					height: this.props.size.height,
				},
			},
			React.DOM.div({className: 'cam-blobitem-bsky-head'},
				kind.action ? React.DOM.span({className: 'cam-blobitem-bsky-action'}, kind.action) : null,
				this.props.date ? React.DOM.span({className: 'cam-blobitem-bsky-date'}, cam.dateUtils.formatDateShort(this.props.date)) : null
			),
			isProfile ? this.getProfile_() : this.getPost_(),
			React.DOM.img({className: 'cam-blobitem-bsky-logo', src: 'bluesky-logo.svg', alt: 'Bluesky'})
		);
	},

	getPost_: function() {
		if (this.props.missing) {
			return React.DOM.div({className: 'cam-blobitem-bsky-missing'}, this.props.missing);
		}
		return [
			this.props.author ? React.DOM.div({key: 'author', className: 'cam-blobitem-bsky-author'}, '@' + this.props.author) : null,
			this.props.reply ? React.DOM.div({key: 'reply', className: 'cam-blobitem-bsky-reply'}, 'Reply') : null,
			this.props.text ? React.DOM.div({key: 'text', className: 'cam-blobitem-bsky-text'}, this.props.text) : null,
			this.props.linkTitle ? React.DOM.div({key: 'link', className: 'cam-blobitem-bsky-link'}, this.props.linkTitle) : null,
			this.props.image ? React.DOM.div({
				key: 'image',
				className: 'cam-blobitem-bsky-image',
				style: {backgroundImage: 'url(' + this.props.image + ')'},
			}) : null,
		];
	},

	getProfile_: function() {
		if (this.props.missing) {
			return React.DOM.div({className: 'cam-blobitem-bsky-missing'}, this.props.missing);
		}
		return React.DOM.div({className: 'cam-blobitem-bsky-profile'},
			this.props.image ? React.DOM.div({
				className: 'cam-blobitem-bsky-avatar',
				style: {backgroundImage: 'url(' + this.props.image + ')'},
			}) : null,
			this.props.name ? React.DOM.div({className: 'cam-blobitem-bsky-name'}, this.props.name) : null,
			this.props.author ? React.DOM.div({className: 'cam-blobitem-bsky-author'}, '@' + this.props.author) : null
		);
	},
});

// The camliNodeType values of the importer's permanodes.
cam.BlobItemBlueskyContent.POST = 'bluesky:app.bsky.feed.post';
cam.BlobItemBlueskyContent.LINKED_POST = 'bluesky:linkedPost'; // another's, that a like or repost is of
cam.BlobItemBlueskyContent.PROFILE = 'bluesky:profile';

// KINDS are the kinds of permanodes shown, by name: their camliNodeType
// values, and, for those of others' posts or profiles, the action they
// show above them.
cam.BlobItemBlueskyContent.KINDS = {
	post: {type: cam.BlobItemBlueskyContent.POST},
	linkedPost: {type: cam.BlobItemBlueskyContent.LINKED_POST},
	like: {type: 'bluesky:app.bsky.feed.like', action: '♥ Liked'},
	repost: {type: 'bluesky:app.bsky.feed.repost', action: '↻ Reposted'},
	follow: {type: 'bluesky:app.bsky.graph.follow', action: 'Followed'},
	profile: {type: cam.BlobItemBlueskyContent.PROFILE},
};

// kindOf returns the name of the kind of permanode of the camliNodeType
// typ, or the empty string if it's not one shown.
cam.BlobItemBlueskyContent.kindOf = function(typ) {
	for (var k in cam.BlobItemBlueskyContent.KINDS) {
		if (cam.BlobItemBlueskyContent.KINDS[k].type == typ) {
			return k;
		}
	}
	return '';
};

cam.BlobItemBlueskyContent.getHandler = function(blobref, searchSession, href) {
	var m = searchSession.getMeta(blobref);
	if (m.camliType != 'permanode') {
		return null;
	}
	var kind = cam.BlobItemBlueskyContent.kindOf(cam.permanodeUtils.getCamliNodeType(m.permanode));
	if (!kind) {
		return null;
	}
	var props = {kind: kind};
	var date = Date.parse(cam.permanodeUtils.getSingleAttr(m.permanode, 'startDate'));
	if (date) {
		props.date = date;
	}
	// What a like, repost or follow is of: the post or profile that is its
	// content, if the importer fetched it.
	var subject = m.permanode;
	if (kind == 'like' || kind == 'repost' || kind == 'follow') {
		// Others' posts, or the account's own, for reposts of them.
		var want = kind == 'follow' ? [cam.BlobItemBlueskyContent.PROFILE] :
			[cam.BlobItemBlueskyContent.LINKED_POST, cam.BlobItemBlueskyContent.POST];
		var cm = searchSession.getResolvedMeta(blobref);
		subject = null;
		if (cm && cm.camliType == 'permanode' && want.indexOf(cam.permanodeUtils.getCamliNodeType(cm.permanode)) >= 0) {
			subject = cm.permanode;
		}
	}
	if (!subject) {
		// The importer marks a like, repost or follow done, with its
		// record's CID, once it has looked for what it's of, which it
		// didn't find if it's not its content.
		var what = kind == 'follow' ? 'profile' : 'post';
		props.missing = cam.permanodeUtils.getSingleAttr(m.permanode, 'atprotoCID') ?
			'The ' + what + ' wasn’t found.' : 'The ' + what + ' wasn’t fetched.';
		return new cam.BlobItemBlueskyContent.Handler(href, props, null);
	}
	var attr = function(name) {
		return cam.permanodeUtils.getSingleAttr(subject, name) || '';
	};
	props.author = attr('handle');
	if (kind == 'follow' || kind == 'profile') {
		props.name = attr('name') || (kind == 'profile' ? attr('title') : '');
	} else {
		props.text = attr('content');
		props.linkTitle = attr('linkTitle');
		props.reply = !!attr('inReplyTo');
	}
	var imageRef = attr('camliContentImage');
	var imageMeta = imageRef ? searchSession.getResolvedMeta(imageRef) : null;
	return new cam.BlobItemBlueskyContent.Handler(href, props, imageMeta ? cam.Thumber.fromImageMeta(imageMeta) : null);
};

cam.BlobItemBlueskyContent.Handler = function(href, props, thumber) {
	this.href_ = href;
	this.props_ = props;
	this.thumber_ = thumber;
};

cam.BlobItemBlueskyContent.Handler.prototype.getAspectRatio = function() {
	return 1.0;
};

cam.BlobItemBlueskyContent.Handler.prototype.createContent = function(size) {
	var props = goog.object.clone(this.props_);
	props.href = this.href_;
	props.size = size;
	if (this.thumber_) {
		props.image = this.thumber_.getSrc(size);
	}
	return React.createElement(cam.BlobItemBlueskyContent, props);
};
