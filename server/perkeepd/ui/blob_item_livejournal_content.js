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

goog.provide('cam.BlobItemLiveJournalContent');

goog.require('goog.math.Size');

goog.require('cam.dateUtils');
goog.require('cam.permanodeUtils');

// BlobItemLiveJournalContent shows a LiveJournal entry, as pk livejournal
// keeps them: its date, its subject if it has one, and the start of its
// text.
cam.BlobItemLiveJournalContent = React.createClass({
	propTypes: {
		href: React.PropTypes.string.isRequired,
		size: React.PropTypes.instanceOf(goog.math.Size).isRequired,
		date: React.PropTypes.number.isRequired,
		title: React.PropTypes.string,
		excerpt: React.PropTypes.string.isRequired,
		security: React.PropTypes.string,
	},

	render: function() {
		var when = cam.dateUtils.formatDateShort(this.props.date);
		if (this.props.security) {
			when += ' · ' + this.props.security;
		}
		return React.DOM.a({
				href: this.props.href,
				className: 'cam-blobitem-lj-entry',
				style: {
					width: this.props.size.width,
					height: this.props.size.height,
				},
			},
			React.DOM.div({className: 'cam-blobitem-lj-date'}, when),
			this.props.title ? React.DOM.div({className: 'cam-blobitem-lj-title'}, this.props.title) : null,
			React.DOM.div({className: 'cam-blobitem-lj-text'}, this.props.excerpt),
			React.DOM.img({className: 'cam-blobitem-lj-logo', src: 'livejournal-logo.png', alt: 'LiveJournal'})
		);
	},
});

// How much of an entry's text to show, with and without a subject.
cam.BlobItemLiveJournalContent.EXCERPT_CHARS = 280;
cam.BlobItemLiveJournalContent.EXCERPT_CHARS_UNTITLED = 560;

// Returns the plain text of html, a LiveJournal entry's text: LiveJournal
// shows its newlines as line breaks, and its own <lj user=name> tags as
// the names. The HTML is only parsed (in an inert document), never shown.
cam.BlobItemLiveJournalContent.textOf = function(html) {
	html = html.
		replace(/<lj\s+(?:user|comm)\s*=\s*["']?([\w-]+)["']?\s*\/?>/gi, '$1').
		replace(/<lj-cut[^>]*>/gi, '\n').
		replace(/<br\s*\/?>/gi, '\n').
		replace(/<\/p\s*>/gi, '\n\n');
	var doc = new DOMParser().parseFromString(html, 'text/html');
	var text = doc.body ? doc.body.textContent : html;
	return text.replace(/[ \t]+/g, ' ').replace(/\n[ \t]+/g, '\n').replace(/\n{3,}/g, '\n\n').trim();
};

cam.BlobItemLiveJournalContent.excerpt = function(text, max) {
	if (text.length <= max) {
		return text;
	}
	var cut = text.lastIndexOf(' ', max);
	return text.substring(0, cut > max / 2 ? cut : max) + '…';
};

cam.BlobItemLiveJournalContent.getHandler = function(blobref, searchSession, href) {
	var m = searchSession.getMeta(blobref);
	if (m.camliType != 'permanode' ||
		cam.permanodeUtils.getCamliNodeType(m.permanode) != 'livejournal.com:entry') {
		return null;
	}
	var date = Date.parse(cam.permanodeUtils.getSingleAttr(m.permanode, 'startDate'));
	if (!date) {
		return null;
	}
	var title = cam.permanodeUtils.getSingleAttr(m.permanode, 'title') || '';
	var text = cam.BlobItemLiveJournalContent.textOf(cam.permanodeUtils.getSingleAttr(m.permanode, 'content') || '');
	var max = title ? cam.BlobItemLiveJournalContent.EXCERPT_CHARS : cam.BlobItemLiveJournalContent.EXCERPT_CHARS_UNTITLED;
	var security = cam.permanodeUtils.getSingleAttr(m.permanode, 'livejournalSecurity');
	var securityLabel = {private: 'private', usemask: 'friends-only'}[security] || '';
	return new cam.BlobItemLiveJournalContent.Handler(href, date, title,
		cam.BlobItemLiveJournalContent.excerpt(text, max), securityLabel);
};

cam.BlobItemLiveJournalContent.Handler = function(href, date, title, excerpt, security) {
	this.href_ = href;
	this.date_ = date;
	this.title_ = title;
	this.excerpt_ = excerpt;
	this.security_ = security;
};

cam.BlobItemLiveJournalContent.Handler.prototype.getAspectRatio = function() {
	return 1.0;
};

cam.BlobItemLiveJournalContent.Handler.prototype.createContent = function(size) {
	return React.createElement(cam.BlobItemLiveJournalContent, {
		href: this.href_,
		size: size,
		date: this.date_,
		title: this.title_,
		excerpt: this.excerpt_,
		security: this.security_,
	});
};
