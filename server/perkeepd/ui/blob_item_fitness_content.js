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

goog.provide('cam.BlobItemFitnessContent');

goog.require('goog.math.Size');

goog.require('cam.permanodeUtils');
goog.require('cam.Thumber');

// Fitness cards use generic activity metadata and locally stored track previews.
// The thumbnail server draws the route and its activity badge; no map or provider
// requests are made by this component.
cam.BlobItemFitnessContent = React.createClass({
	displayName: 'BlobItemFitnessContent',

	propTypes: {
		href: React.PropTypes.string.isRequired,
		size: React.PropTypes.instanceOf(goog.math.Size).isRequired,
		src: React.PropTypes.string.isRequired,
		activity: React.PropTypes.object.isRequired,
	},

	getInitialState: function() {
		return {failed: false};
	},

	componentWillReceiveProps: function(nextProps) {
		if (nextProps.src != this.props.src) {
			this.setState({failed: false});
		}
	},

	onThumbError_: function() {
		this.setState({failed: true});
	},

	render: function() {
		var activity = this.props.activity;
		var layout = cam.BlobItemFitnessContent.getLayout(this.props.size, activity);
		var attribution = cam.BlobItemFitnessContent.getMapAttribution();
		var tiny = this.props.size.width < 100;
		// Icons keep the linked provider name readable above the sport badge.
		// Custom license text is never shortened; very small custom-provider
		// cards may require a larger preview for legibility. The downloaded PNG
		// retains its own attribution whenever it contains a map.
		var attributionText = attribution && tiny && attribution.text == '\u00a9 OpenStreetMap contributors' ? 'OpenStreetMap' : attribution && attribution.text;
		var hasPreview = this.props.src && !this.state.failed;
		// The bitmap has a compact vector badge; the image-error placeholder
		// uses a text badge, which can be wider for types such as Workout.
		var fallbackBadgeWidth = Math.max(45, Math.ceil(activity.typeLabel.length * layout.metricFont * 0.8 + 14));
		var preview = hasPreview
			? React.DOM.img({
				className: 'cam-blobitem-fitness-image',
				src: this.props.src,
				alt: activity.fullTitle,
				title: activity.fullTitle,
				onError: this.onThumbError_,
				width: this.props.size.width,
				height: layout.previewHeight,
			})
			: React.DOM.div({className: 'cam-blobitem-fitness-placeholder', role: 'img', 'aria-label': activity.typeLabel + ' activity: route preview unavailable'},
				React.DOM.svg({viewBox: '0 0 64 48', 'aria-hidden': 'true', focusable: 'false'},
					React.DOM.polyline({points: '4,25 18,25 25,10 35,39 42,25 60,25', fill: 'none', stroke: 'currentColor', strokeWidth: 3, strokeLinejoin: 'round', strokeLinecap: 'round'})
				),
				React.DOM.span({className: 'cam-blobitem-fitness-fallback-badge', style: {fontSize: layout.metricFont, lineHeight: layout.metricLine + 'px'}}, activity.typeLabel)
			);
		return React.DOM.div({
				className: 'cam-blobitem-fitness',
				style: {width: this.props.size.width, height: this.props.size.height},
			},
			React.DOM.div({className: 'cam-blobitem-fitness-preview', style: {height: layout.previewHeight}},
				React.DOM.a({className: 'cam-blobitem-fitness-preview-link', href: this.props.href, title: activity.fullTitle, 'aria-label': activity.fullTitle}, preview),
				attribution ? React.DOM.a({
					className: 'cam-blobitem-fitness-attribution' + (tiny ? ' cam-blobitem-fitness-attribution-tiny' : ''),
					href: attribution.href,
					target: '_blank',
					rel: 'noopener',
					title: attribution.text,
					'aria-label': attribution.text,
					style: !tiny && !hasPreview ? {maxWidth: 'calc(100% - ' + fallbackBadgeWidth + 'px)'} : null,
				}, attributionText) : null
			),
			React.DOM.a({className: 'cam-blobitem-fitness-footer', href: this.props.href, title: activity.fullTitle, 'aria-label': activity.typeLabel + (activity.metrics ? ' \u00b7 ' + activity.metrics : ''), style: {height: layout.footerHeight, padding: layout.padding}},
				React.DOM.div({className: 'cam-blobitem-fitness-name', style: {fontSize: layout.titleFont, lineHeight: layout.titleLine + 'px'}}, activity.title),
				React.DOM.div({className: 'cam-blobitem-fitness-metrics', style: {fontSize: layout.metricFont, lineHeight: layout.metricLine + 'px'}}, activity.metrics || activity.typeLabel),
				layout.showDate ? React.DOM.div({className: 'cam-blobitem-fitness-date', style: {fontSize: layout.metricFont, lineHeight: layout.metricLine + 'px'}}, activity.typeLabel + ' \u00b7 ' + activity.date) : null
			)
		);
	},
});

cam.BlobItemFitnessContent.TYPE_LABELS = {
	run: 'Run',
	walk: 'Walk',
	hike: 'Hike',
	ride: 'Ride',
	swim: 'Swim',
	row: 'Row',
	ski: 'Ski',
	workout: 'Workout',
	other: 'Other',
};

// Attribution is deliberately present even if the server fell back to a grid.
// Only the local thumbnail is fetched; this native link never loads map tiles.
cam.BlobItemFitnessContent.getMapAttribution = function() {
	var config = goog.global.CAMLISTORE_CONFIG;
	if (!config || config.fitnessMapEnabled !== true) {
		return null;
	}
	var href = 'https://www.openstreetmap.org/copyright';
	var configuredURL = config.fitnessMapAttributionURL;
	if (typeof configuredURL == 'string' && /^https?:\/\//i.test(configuredURL) && !/^https?:\/\/[^/?#]*@/i.test(configuredURL) && !/[\s\u0000-\u001f\u007f]/.test(configuredURL)) {
		try {
			var url = new URL(configuredURL);
			if ((url.protocol == 'http:' || url.protocol == 'https:') && url.hostname && !url.username && !url.password) {
				href = url.href;
			}
		} catch (error) {
			// Malformed discovery values must not become executable links.
		}
	}
	return {
		text: typeof config.fitnessMapAttribution == 'string' && config.fitnessMapAttribution.trim() ? config.fitnessMapAttribution : '\u00a9 OpenStreetMap contributors',
		href: href,
	};
};

// Do not accept parseFloat's partial numbers, empty strings, hex, or infinities.
// Zero is deliberately distinct from missing or invalid metadata.
cam.BlobItemFitnessContent.parseNumber = function(value) {
	if (typeof value == 'string') {
		value = value.trim();
		if (!/^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/.test(value)) {
			return null;
		}
		value = Number(value);
	}
	return typeof value == 'number' && isFinite(value) ? value : null;
};

cam.BlobItemFitnessContent.nonnegativeNumber = function(value) {
	var number = cam.BlobItemFitnessContent.parseNumber(value);
	return number != null && number >= 0 ? number : null;
};

// This deliberately rough start-position rectangle is not a country lookup.
cam.BlobItemFitnessContent.isUSStart = function(latitude, longitude) {
	var lat = cam.BlobItemFitnessContent.parseNumber(latitude);
	var lon = cam.BlobItemFitnessContent.parseNumber(longitude);
	return lat != null && lon != null && lat >= 24 && lat <= 50 && lon >= -125 && lon <= -66;
};

cam.BlobItemFitnessContent.formatDistance = function(value, latitude, longitude) {
	var meters = cam.BlobItemFitnessContent.nonnegativeNumber(value);
	if (meters == null) {
		return '';
	}
	var kilometers = meters / 1000;
	// This is a display-format limit, not a schema constraint: toFixed switches
	// to scientific notation at 1e21, which cannot promise one decimal place.
	if (kilometers >= 1e21) {
		return '';
	}
	var distance = kilometers.toFixed(1) + ' km';
	if (cam.BlobItemFitnessContent.isUSStart(latitude, longitude)) {
		distance += ' / ' + (meters / 1609.344).toFixed(1) + ' mi';
	}
	return distance;
};

cam.BlobItemFitnessContent.formatDuration = function(moving, elapsed) {
	var seconds = cam.BlobItemFitnessContent.nonnegativeNumber(moving);
	if (seconds == null) {
		seconds = cam.BlobItemFitnessContent.nonnegativeNumber(elapsed);
	}
	if (seconds == null) {
		return '';
	}
	seconds = Math.floor(seconds);
	var hours = Math.floor(seconds / 3600);
	var minutes = Math.floor(seconds % 3600 / 60);
	var remainder = seconds % 60;
	if (hours) {
		return hours + 'h' + (minutes ? ' ' + minutes + 'm' : '');
	}
	if (minutes) {
		return minutes + 'm' + (remainder ? ' ' + remainder + 's' : '');
	}
	return remainder + 's';
};

cam.BlobItemFitnessContent.formatDate = function(value) {
	if (typeof value != 'string' || !/^\d{4}-\d{2}-\d{2}T/.test(value)) {
		return '';
	}
	var timestamp = Date.parse(value);
	if (!isFinite(timestamp)) {
		return '';
	}
	var date = new Date(timestamp);
	var months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
	return months[date.getUTCMonth()] + ' ' + date.getUTCDate() + ', ' + date.getUTCFullYear();
};

cam.BlobItemFitnessContent.getActivity = function(permanode) {
	var attr = function(name) {
		return cam.permanodeUtils.getSingleAttr(permanode, name);
	};
	var type = attr('fitnessActivityType');
	type = typeof type == 'string' && type ? type : 'other';
	var typeLabel = Object.prototype.hasOwnProperty.call(cam.BlobItemFitnessContent.TYPE_LABELS, type) ? cam.BlobItemFitnessContent.TYPE_LABELS[type] : 'Other';
	var title = attr('title');
	title = typeof title == 'string' && title.trim() ? title : (typeLabel == 'Other' ? 'Fitness activity' : typeLabel);
	var track = attr('fitnessTrack');
	track = typeof track == 'string' ? track : '';
	var distance = cam.BlobItemFitnessContent.formatDistance(attr('fitnessDistance'), attr('latitude'), attr('longitude'));
	var duration = cam.BlobItemFitnessContent.formatDuration(attr('fitnessMovingDuration'), attr('fitnessElapsedDuration'));
	var metrics = [distance, duration].filter(function(part) { return !!part; }).join(' \u00b7 ');
	var date = cam.BlobItemFitnessContent.formatDate(attr('startDate'));
	return {
		type: type,
		typeLabel: typeLabel,
		title: title,
		track: track,
		metrics: metrics,
		date: date,
		fullTitle: [title, metrics, typeLabel, date].filter(function(part) { return !!part; }).join(' \u00b7 '),
	};
};

// Keep all content within the size assigned by the virtualized grid. Small
// thumbnails retain two readable lines; a third type/date line is optional.
cam.BlobItemFitnessContent.getLayout = function(size, activity) {
	var titleFont = Math.max(9, Math.min(14, size.width / 12));
	var metricFont = Math.max(8, Math.min(12, size.width / 14));
	var titleLine = Math.ceil(titleFont * 1.2);
	var metricLine = Math.ceil(metricFont * 1.2);
	var padding = Math.max(2, Math.min(8, Math.floor(size.width / 24)));
	var showDate = !!activity.date && size.width >= 180 && size.height >= 150;
	var footerHeight = Math.min(size.height, titleLine + metricLine * (showDate ? 2 : 1) + padding * 2);
	return {
		titleFont: titleFont,
		metricFont: metricFont,
		titleLine: titleLine,
		metricLine: metricLine,
		padding: padding,
		showDate: showDate,
		footerHeight: footerHeight,
		previewHeight: Math.max(0, size.height - footerHeight),
	};
};

cam.BlobItemFitnessContent.getThumbnailURL = function(blobref, track, type, size, opt_dpr, opt_uiRoot) {
	var dpr = cam.BlobItemFitnessContent.parseNumber(opt_dpr);
	dpr = dpr != null ? Math.max(1, Math.min(2, dpr)) : 1;
	var width = Math.max(1, size.width);
	var height = Math.max(1, size.height);
	var longest = Math.max(width, height);
	var requested = Math.min(2000, longest * dpr);
	var bucket = 2000;
	for (var i = 0; i < cam.Thumber.SIZES.length; i++) {
		if (cam.Thumber.SIZES[i] >= requested) {
			bucket = Math.min(2000, cam.Thumber.SIZES[i]);
			break;
		}
	}
	var mw = Math.max(1, Math.min(2000, Math.round(bucket * width / longest)));
	var mh = Math.max(1, Math.min(2000, Math.round(bucket * height / longest)));
	var config = goog.global.CAMLISTORE_CONFIG;
	var root = opt_uiRoot != null ? opt_uiRoot : (config && config.uiRoot || '/ui/');
	if (root.charAt(root.length - 1) != '/') {
		root += '/';
	}
	// Trackless activities still get a server-rendered neutral preview and sport
	// badge. These query values partition the cache; the server reads real attrs.
	var mapProfile = config && config.fitnessMapEnabled === true ? '&mapProfile=' + encodeURIComponent(typeof config.fitnessMapProfile == 'string' ? config.fitnessMapProfile : '') : '';
	return root + 'thumbnail/' + encodeURIComponent(blobref) + '/track.png?mw=' + mw + '&mh=' + mh + '&fitnessTrack=' + encodeURIComponent(track || '') + '&fitnessActivityType=' + encodeURIComponent(type || 'other') + '&ftv=3' + mapProfile;
};

cam.BlobItemFitnessContent.getHandler = function(blobref, searchSession, href) {
	var meta = searchSession.getMeta(blobref);
	if (!meta || meta.camliType != 'permanode' || !meta.permanode || !meta.permanode.attr || cam.permanodeUtils.getCamliNodeType(meta.permanode) != 'fitnessActivity') {
		return null;
	}
	return new cam.BlobItemFitnessContent.Handler(blobref, searchSession, href);
};

cam.BlobItemFitnessContent.Handler = function(blobref, searchSession, href) {
	this.blobref_ = blobref;
	this.searchSession_ = searchSession;
	this.href_ = href;
};

cam.BlobItemFitnessContent.Handler.prototype.getAspectRatio = function() {
	return 1.15;
};

cam.BlobItemFitnessContent.Handler.prototype.createContent = function(size) {
	// Read live metadata rather than retaining a stale track/type cache key.
	var meta = this.searchSession_.getMeta(this.blobref_);
	var activity = cam.BlobItemFitnessContent.getActivity(meta && meta.permanode && meta.permanode.attr ? meta.permanode : {attr: {}});
	var layout = cam.BlobItemFitnessContent.getLayout(size, activity);
	return React.createElement(cam.BlobItemFitnessContent, {
		href: this.href_,
		size: size,
		activity: activity,
		src: cam.BlobItemFitnessContent.getThumbnailURL(this.blobref_, activity.track, activity.type, new goog.math.Size(size.width, layout.previewHeight), goog.global.devicePixelRatio),
	});
};
