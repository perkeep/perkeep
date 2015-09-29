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

goog.provide('cam.BlobItemFoursquareContent');

goog.require('goog.array');
goog.require('goog.math.Size');
goog.require('goog.object');
goog.require('goog.string');

goog.require('cam.dateUtils');
goog.require('cam.math');
goog.require('cam.permanodeUtils');
goog.require('cam.Thumber');

cam.BlobItemFoursquareContent = React.createClass({
	propTypes: {
		href: React.PropTypes.string.isRequired,
		size: React.PropTypes.instanceOf(goog.math.Size).isRequired,
		venueId: React.PropTypes.string.isRequired,
		venueName: React.PropTypes.string.isRequired,
		photo: React.PropTypes.string.isRequired,
		date: React.PropTypes.number.isRequired,
	},

	render: function() {
		return React.DOM.a({
				href: this.props.href,
				className: 'cam-blobitem-fs-checkin',
				style: {
					backgroundImage: 'url(' + this.props.photo + ')',
					width: this.props.size.width,
					height: this.props.size.height,
				},
			},
			React.DOM.div({className:'cam-blobitem-fs-checkin-content'},
				React.DOM.img({src: 'foursquare-logo.png'}),
				React.DOM.table(null,
					React.DOM.tr(null,
						React.DOM.td(null,
							React.DOM.div({className:'cam-blobitem-fs-checkin-intro'}, 'Check-in at'),
							React.DOM.div({className:'cam-blobitem-fs-checkin-venue'}, this.props.venueName)
						)
					)
				),
				React.DOM.div({className:'cam-blobitem-fs-checkin-when'}, cam.dateUtils.formatDateShort(this.props.date))
			)
		);
	},
});

// Blech, we need this to prevent images from flashing when data changes server-side.
cam.BlobItemFoursquareContent.photoMeta_ = {};

cam.BlobItemFoursquareContent.getPhotoMeta_ = function(blobref, venueMeta, searchSession) {
	var photoMeta = this.photoMeta_[blobref];
	if (photoMeta) {
		return photoMeta;
	}

	var photosBlobref = cam.permanodeUtils.getSingleAttr(venueMeta.permanode, 'camliPath:photos')
	var photosMeta = searchSession.getMeta(photosBlobref);
	var photoIds = (photosMeta && photosMeta.permanode && goog.object.getKeys(photosMeta.permanode.attr).filter(function(k) { return goog.string.startsWith(k, 'camliPath:') })) || [];

	photoMeta = (photoIds.length && cam.permanodeUtils.getSingleAttr(photosMeta.permanode, photoIds[goog.string.hashCode(blobref) % photoIds.length])) || null;
	if (photoMeta) {
		photoMeta = this.photoMeta_[blobref] = searchSession.getMeta(photoMeta);
	}

	return photoMeta;
};

cam.BlobItemFoursquareContent.getHandler = function(blobref, searchSession, href) {
	var m = searchSession.getMeta(blobref);
	if (m.camliType != 'permanode') {
		return null;
	}

	if (cam.permanodeUtils.getCamliNodeType(m.permanode) != 'foursquare.com:checkin') {
		return null;
	}

	var startDate = cam.permanodeUtils.getSingleAttr(m.permanode, 'startDate');
	var venueBlobref = cam.permanodeUtils.getSingleAttr(m.permanode, 'foursquareVenuePermanode');
	if (!startDate || !venueBlobref) {
		return null;
	}


	var venueMeta = searchSession.getResolvedMeta(venueBlobref);
	if (!venueMeta) {
		return null;
	}

	var venueId = cam.permanodeUtils.getSingleAttr(venueMeta.permanode, 'foursquareId');
	var venueName = cam.permanodeUtils.getSingleAttr(venueMeta.permanode, 'title');
	if (!venueId || !venueName) {
		return null;
	}

	return new cam.BlobItemFoursquareContent.Handler(href, venueId, venueName,
		cam.BlobItemFoursquareContent.getPhotoMeta_(blobref, venueMeta, searchSession), Date.parse(startDate));
};

cam.BlobItemFoursquareContent.Handler = function(href, venueId, venueName, venuePhotoMeta, startDate) {
	this.href_ = href;
	this.venueId_ = venueId;
	this.venueName_ = venueName;
	this.startDate_ = startDate;
	this.thumber_ = venuePhotoMeta ? new cam.Thumber.fromImageMeta(venuePhotoMeta) : null;
};

cam.BlobItemFoursquareContent.Handler.prototype.getAspectRatio = function() {
	return 1.0;
};

cam.BlobItemFoursquareContent.Handler.prototype.createContent = function(size) {
	return cam.BlobItemFoursquareContent({
		href: this.href_,
		size: size,
		venueId: this.venueId_,
		venueName: this.venueName_,
		photo: this.thumber_ ? this.thumber_.getSrc(size) : '',
		date: this.startDate_,
	});
};
