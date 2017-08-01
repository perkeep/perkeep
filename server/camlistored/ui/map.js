/*
Copyright 2017 The Camlistore Authors

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

goog.provide('cam.MapAspect');

goog.require('cam.SearchSession');
goog.require('cam.Thumber');

cam.MapAspect = React.createClass({
	QUERY_LIMIT_: 1000,

	propTypes: {
		availWidth: React.PropTypes.number.isRequired,
		availHeight: React.PropTypes.number.isRequired,
		searchSession: React.PropTypes.instanceOf(cam.SearchSession).isRequired,
		config: React.PropTypes.object.isRequired,
	},

	componentWillMount: function() {
		this.latitude = 0.0;
		this.longitude = 0.0;
		this.location = {
			North: 0.0,
			South: 0.0,
			East: 0.0,
			West: 0.0,
		};
		this.markers = {};
		this.mapQuery = null;
		this.eh_ = new goog.events.EventHandler(this);
	},

	// setCoordinatesFromSearchQuery looks into the search session query for obvious
	// geographic coordinates. Either a location predicate ("loc:seattle"), or a
	// location area predicate ("locrect:48.63,-123.37,46.59,-121.28") are considered
	// for now.
	setCoordinatesFromSearchQuery: function() {
		var ss = this.props.searchSession;
		if (!ss) {
			return;
		}
		// TODO(mpl): it is so disgusting that ss.query_ can be both a string or an
		// object. But that's something to fix in search session, with deep repercussions.
		if (ss.isEmptyQuery()) {
			return;
		}
		var q = ss.getQueryExprOrRef()
		// TODO(mpl): support "ref:sha1-foobar" predicate. Needs server-side first.
		if (q == '') {
			return;
		}
		if (goreact.HandleLocAreaPredicate(q, this.handleCoordinatesFound)) {
			// a "locrect" area query
			return;
		}
		if (goreact.IsLocPredicate(q)) {
			// a "loc" query
			goreact.Geocode(q.substring(goreact.LocPredicatePrefix.length), this.handleCoordinatesFound);
			return;
		}
		// Not a location type query
		this.refreshMapView();
	},

	// handleCoordinatesFound sets this.location (a rectangle), this.latitude, and
	// this.longitude (center of this.location), from the given rectangle.
	handleCoordinatesFound: function(rect) {
		if (!rect) {
			return;
		}
		var eastWest = goreact.WrapAntimeridian(rect.East, rect.West);
		rect.West = eastWest.W;
		rect.East = eastWest.E;
		if (this.sameLocations(rect, this.location)) {
			return;
		}
		this.location = rect;
		L.rectangle([[this.location.North, this.location.East],[this.location.South,this.location.West]], {color: "#ff7800", weight: 1}).addTo(this.map);
		var center = goreact.LocationCenter(this.location.North, this.location.South, this.location.West, this.location.East);
		this.latitude = center.Lat;
		this.longitude = center.Long;
		this.locationFound = true;
		this.refreshMapView();
		return;
	},

	// refreshMapView pans to the relevant coordinates found for the current search
	// session, if any. Otherwise, pan to englobe all the markers that were drawn.
	refreshMapView: function() {
		if (!this.locationFound && !this.locationFromMarkers) {
			return;
		}
		if (this.locationFound) {
			// pan to the location we found in the search query itself.
			var location = L.latLngBounds(L.latLng(this.location.North, this.location.East),
				L.latLng(this.location.South, this.location.West));
		} else {
			// otherwise, fit the view to encompass all the markers that were drawn
			var location = this.locationFromMarkers;
		}
		this.map.fitBounds(location);
		this.mapQuery.SetZoom(location.getNorth(), location.getWest(), location.getSouth(), location.getEast());
		this.props.onZoomLevelChange(this.mapQuery.GetExpr());
	},

	sameLocations: function(loc1, loc2) {
		return (loc1.North == loc2.North &&
			loc1.South == loc2.South &&
			loc1.West == loc2.West &&
			loc1.East == loc2.East)
	},

	// loadMarkers sets markers on the map for all the permanodes, with a location,
	// found in the current search session.
	loadMarkers: function() {
		var ss = this.props.searchSession;
		if (!ss) {
			return;
		}
		if (ss.isEmptyQuery()) {
			console.log("refusing to load markers for an empty search query");
			return;
		}
		var q = ss.getQueryExprOrRef()
		// TODO(mpl): support "ref:sha1-foobar" predicate. Needs server-side first.
		if (q == '') {
			return;
		}
		if (this.mapQuery == null) {
			this.mapQuery = goreact.NewMapQuery(this.props.config.authToken, q, this.handleSearchResults);
			this.mapQuery.SetLimit(this.QUERY_LIMIT_);
		}
		this.mapQuery.Send();
	},

	// TODO(mpl): if we add caching of the results to the gopherjs searchsession,
	// then getMeta, getResolvedMeta, and getTitle can become methods on the Session
	// type, and we can remove the searchResults argument.

	getMeta: function(br, searchResults) {
		if (!searchResults || !searchResults.description || !searchResults.description.meta) {
			return null;
		}
		return searchResults.description.meta[br];
	},

	getResolvedMeta: function(br, searchResults) {
		var meta = this.getMeta(br, searchResults);
		if (!meta) {
			return null;
		}
		if (meta.camliType == 'permanode') {
			var camliContent = cam.permanodeUtils.getSingleAttr(meta.permanode, 'camliContent');
			if (camliContent) {
				return searchResults.description.meta[camliContent];
			}
		}
		return meta;
	},

	getTitle: function(br, searchResults) {
		var meta = this.getMeta(br, searchResults);
		if (!meta) {
			return '';
		}
		if (meta.camliType == 'permanode') {
			var title = cam.permanodeUtils.getSingleAttr(meta.permanode, 'title');
			if (title) {
				return title;
			}
		}
		var rm = this.getResolvedMeta(br, searchResults);
		return (rm && rm.camliType == 'file' && rm.file.fileName) || (rm && rm.camliType == 'directory' && rm.dir.fileName) || '';
	},

	handleSearchResults: function(searchResultsJSON) {
		var searchResults = JSON.parse(searchResultsJSON);
		var blobs = searchResults.blobs;
		var icon = L.icon({
			iconUrl: this.props.config.uiRoot + 'leaflet/marker-icon.png'
		});
		blobs.forEach(function(b) {
			var br = b.blob;
			var marker = this.markers[br];
			if (marker) {
				// marker has already been loaded on the map
				return;
			}
			var m = this.getResolvedMeta(br, searchResults);
			if (!m || !m.location) {
				var pm = this.getMeta(br, searchResults);
				if (!pm || !pm.location) {
					return;
				}
				// permanode itself has a location (not its contents)
				var location = pm.location;
			} else {
				// contents, camliPath, etc has a location
				var location = m.location;
			}

			var marker = L.marker([location.latitude, location.longitude], {icon: icon});

			// TODO(mpl): The piece of code below is temporarily commented out because the
			// awesome markers seem to be breaking the UI in a way that I don't understand yet.
			// Symptoms are: tiles not loading properly, aspect names (top right) disappearing
			// when hovering over them, same for the main drop down menu. First thought was
			// some z-index related bug, but no luck finding such a culprit. Plus it seems
			// related to the number of markers loaded, as the bugs don't appear if there's
			// just a handful of markers.
/*
			// all awesome markers use markers-soft.png (body of the marker), and markers-shadow.png.
			var iconOpts = {
				prefix: 'fa',
				iconColor: 'white',
				markerColor: 'blue'
			};
			// TODO(mpl): twitter, when we handle location for tweets, which I thought we already did.
			if (m.permanode && cam.permanodeUtils.getCamliNodeType(m.permanode) == 'foursquare.com:checkin') {
				iconOpts.icon = 'foursquare';
			} else if (m.image) {
				// image file
				iconOpts.icon = 'camera';
			} else if (m.camliType == 'file') {
				// generic file
				iconOpts.icon = 'file';
			} else {
				// default node
				// TODO(mpl): I used 'circle' because it looks the most like the default leaflet
				// marker-icon.png, but it'd be cool to have something that reminds of the
				// Camlistore "brand". Maybe the head of the eagle on the banner?
				iconOpts.icon = 'circle';
			}
			var markerIcon = L.AwesomeMarkers.icon(iconOpts);
			var marker = L.marker([location.latitude, location.longitude], {icon: markerIcon});
*/

			// Note that we've created that marker already.
			this.markers[br] = true;
			marker.addTo(this.map);
			if (m.image) {
				// TODO(mpl): Do we ever want another thumb size? on mobile maybe?
				var img = cam.Thumber.fromImageMeta(m).getSrc(64);
				marker.bindPopup('<a href="'+this.props.config.uiRoot+br+'"><img src="'+img+'" alt="'+br+'" height="64"></a>');
			} else {
				var title = this.getTitle(br, searchResults);
				if (title != '') {
					marker.bindPopup('<a href="'+this.props.config.uiRoot+br+'">'+title+'</a>');
				} else {
					marker.bindPopup('<a href="'+this.props.config.uiRoot+br+'">'+br+'</a>');
				}
			}
			if (!this.locationFromMarkers) {
				// initialize it as a square of 0.1 degree around the first marker placed
				var northeast = L.latLng(location.latitude + 0.05, location.longitude + 0.05);
				var southwest = L.latLng(location.latitude - 0.05, location.longitude - 0.05);
				this.locationFromMarkers = L.latLngBounds(northeast, southwest);
			} else {
				// then grow locationFromMarkers to englobe the new marker (if needed)
				this.locationFromMarkers.extend(L.latLng(location.latitude, location.longitude));
			}
		}.bind(this));
		this.refreshMapView();
		// TODO(mpl): reintroduce the Around/Continue logic later if needed. For now not
		// needed/useless as MapSorted queries do not support continuation of any kind.
		window.dispatchEvent(new Event('resize'))
	},

	componentDidMount: function() {
		var map = this.map = L.map(ReactDOM.findDOMNode(this), {
			layers: [
				L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', {
					attribution: '©  <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors'
				})
			],
			attributionControl: true,
		});
		map.setView([0., 0.], 3);

		this.eh_.listen(window, 'resize', function(event) {
			// Even after setting the bounds, or the view center+zoom, something is still
			// very wrong, and the map's bounds seem to stay a point (instead of a rectangle).
			// And I can't figure out why. However, any kind of resizing of the window fixes
			// things, so we send a resize event when we're done with loading the markers,
			// and we do one final refreshView here after the resize has happened.
			setTimeout(function(){this.refreshMapView();}.bind(this), 1000);
		});
		this.loadMarkers();
		map.on('click', this.onMapClick);
		this.setCoordinatesFromSearchQuery();
	},

	componentWillUnmount: function() {
		this.map.off('click', this.onMapClick);
		this.eh_.dispose();
		this.map = null;
	},

	onMapClick: function() {
		this.refreshMapView();
	},

	render: function() {
		return React.DOM.div(
			{
				className: 'map',
				style: {
					// we need a low zIndex so that the main piggy scroll menu stays on top of
					// the map when it unfolds.
					zIndex: -1,
					// because of lowest zIndex, this is apparently needed so the map still gets
					// click events. css is dark magic.
					position: 'absolute',
					width: this.props.availWidth,
					height: this.props.availHeight,
				},
			}
		);
	}
});

cam.MapAspect.getAspect = function(config, availWidth, availHeight, onZoomLevelChange,
	childSearchSession, targetBlobRef, parentSearchSession) {
	var searchSession = childSearchSession;
	if (targetBlobRef) {
		// we have a "ref:sha1-foobar" kind of query
		var m = parentSearchSession.getMeta(targetBlobRef);
		if (!m || !m.permanode) {
			return null;
		}

		if (!cam.permanodeUtils.isContainer(m.permanode)) {
			// sha1-foobar is not a container, so we're interested in its own properties,
			// not its children's.
			searchSession = parentSearchSession;
		}
	}

	return {
		fragment: 'map',
		title: 'Map',
		createContent: function(size) {
			return React.createElement(cam.MapAspect, {
				config: config,
				availWidth: availWidth,
				availHeight: availHeight,
				searchSession: searchSession,
				onZoomLevelChange: onZoomLevelChange,
			});
		},
	};
};
