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

// freeze/unfreeze cluster plugin, strongly inspired from
// https://github.com/ghybs/Leaflet.MarkerCluster.Freezable
L.MarkerClusterGroup.include({
	unfreeze: function () {
		this._processQueue();
		if (!this._map) {
			return this;
		}
		this._unfreeze();
		return this;
	},

	freeze: function () {
		this._processQueue();
		if (!this._map) {
			return this;
		}
		this._initiateFreeze();
		return this;
	},

	_initiateFreeze: function () {
		var map = this._map;

		// Start freezing
		this._frozen = true;

		if (map) {
			// Change behaviour on zoomEnd and moveEnd.
			map.off('zoomend', this._zoomEnd, this);
			map.off('moveend', this._moveEnd, this);
		}
	},

	_unfreeze: function () {
		var map = this._map;

		this._frozen = false;

		if (map) {
			// Restore original behaviour on zoomEnd.
			map.on('zoomend', this._zoomEnd, this);
			map.on('moveend', this._moveEnd, this);

			if (this._unspiderfy && this._spiderfied) {
				this._unspiderfy();
			}
			this._zoomEnd();
		}
	}
});


cam.MapAspect = React.createClass({
	// QUERY_LIMIT_ is the maximum number of location markers to draw. It is not
	// arbitrary, as higher numbers (such as 1000) seem to be causing glitches.
	// (https://github.com/camlistore/camlistore/issues/937)
	// However, the cluster plugin restricts the number of items displayed at the
	// same time to a way lower number, allowing us to work-around these glitches.
	QUERY_LIMIT_: 1000,
	// ZOOM_COOLDOWN_ is how much time to wait, after we've stopped zooming/panning,
	// before actually searching for new results.
	ZOOM_COOLDOWN_: 500,

	propTypes: {
		availWidth: React.PropTypes.number.isRequired,
		availHeight: React.PropTypes.number.isRequired,
		searchSession: React.PropTypes.instanceOf(cam.SearchSession).isRequired,
		config: React.PropTypes.object.isRequired,
		updateSearchBar: React.PropTypes.func.isRequired,
		setPendingQuery: React.PropTypes.func.isRequired,
	},

	componentWillMount: function() {
		this.location = {
			North: 0.0,
			South: 0.0,
			East: 0.0,
			West: 0.0,
		};
		this.clusteringOn = this.props.config.mapClustering;
		if (this.clusteringOn == false) {
			// Even 100 is actually too much, and https://github.com/camlistore/camlistore/issues/937 ensues
			this.QUERY_LIMIT_ = 100;
		}
		// isMoving, in conjunction with ZOOM_COOLDOWN_, allows to actually ask for
		// new results only once we've stopped zooming/panning.
		this.isMoving = false;
		this.firstLoad = true;
		this.markers = {};
		if (this.cluster) {
			this.cluster.clearLayers();
		} else if (this.markersGroup) {
			this.markersGroup.clearLayers();
		}
		this.cluster = null;
		this.markersGroup = null;
		this.mapQuery = null;
		this.locationFound = false;
		this.locationFromMarkers = null;
		this.initialSearchSession = this.props.searchSession;
	},

	componentWillReceiveProps: function(nextProps) {
		if (this.props == nextProps) {
			// first load. componentWillMount takes care of the init.
			return;
		}
		if (this.props.searchSession == nextProps.searchSession) {
			// search session has not changed, nothing to do.
			return;
		}
		// Everything below is how we reload from (almost) scratch when a new search is
		// entered in the search box.
		this.componentWillMount();
		this.initialSearchSession = nextProps.searchSession;
		this.loadMarkers();
	},

	componentDidMount: function() {
		this.eh_ = new goog.events.EventHandler(this);
		var map = this.map = L.map(ReactDOM.findDOMNode(this), {
			layers: [
				L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', {
					attribution: '©  <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors'
				})
			],
			attributionControl: true,
			noWrap: true,
		});
		map.setView([0., 0.], 1);

		this.eh_.listen(window, 'resize', function(event) {
			// Even after setting the bounds, or the view center+zoom, something is still
			// very wrong, and the map's bounds seem to stay a point (instead of a rectangle).
			// And I can't figure out why. However, any kind of resizing of the window fixes
			// things, so we send a resize event when we're done with loading the markers,
			// and we do one final refreshView here after the resize has happened.
			setTimeout(function(){this.refreshMapView();}.bind(this), 1000);
		});
		map.on('click', this.onMapClick);
		map.on('zoomend', this.onZoom);
		map.on('moveend', this.onZoom);
		this.loadMarkers();
	},

	componentWillUnmount: function() {
		this.map.off('click', this.onMapClick);
		this.eh_.dispose();
		this.map = null;
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
	},

	// setCoordinatesFromSearchQuery looks into the search session query for obvious
	// geographic coordinates. Either a location predicate ("loc:seattle"), or a
	// location area predicate ("locrect:48.63,-123.37,46.59,-121.28") are considered
	// for now.
	setCoordinatesFromSearchQuery: function() {
		var q = this.initialSearchSession.getQueryExprOrRef();
		if (goreact.IsLocPredicate(q)) {
			// a "loc" query
			goreact.Geocode(q.substring(goreact.LocPredicatePrefix.length), function(rect) {
				return this.handleCoordinatesFound(rect, true);
			}.bind(this));
			return;
		}
		if (goreact.HandleLocAreaPredicate(q, function(rect) {
				return this.handleCoordinatesFound(rect, true);
			}.bind(this))) {
			// a "locrect" area query
			return;
		}
		q = goreact.ShiftMapZoom(q);
		if (goreact.HandleZoomPredicate(q, function(rect) {
				return this.handleCoordinatesFound(rect, false);
			}.bind(this))) {
			// we have a zoom (map:) in the query
			return;
		}
		// Not a location type query
		window.dispatchEvent(new Event('resize'));
	},

	// handleCoordinatesFound sets this.location (a rectangle), this.latitude, and
	// this.longitude (center of this.location), from the given rectangle.
	handleCoordinatesFound: function(rect, draw) {
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
		if (draw) {
			L.rectangle([[this.location.North, this.location.East],[this.location.South,this.location.West]], {color: "#ff7800", weight: 1}).addTo(this.map);
		}
		this.locationFound = true;
		window.dispatchEvent(new Event('resize'));
		return;
	},

	// refreshMapView pans to the relevant coordinates found for the current search
	// session, if any. Otherwise, pan to englobe all the markers that were drawn.
	refreshMapView: function() {
		var zoom = null;
		if (!this.locationFound && !this.locationFromMarkers) {
			if (!this.mapQuery) {
				return;
			}
			zoom = this.mapQuery.GetZoom();
			if (!zoom) {
				return;
			}
		}
		if (zoom) {
			// TODO(mpl): I think we want to remove that case, now that locationFound also
			// takes into account when a "map:" predicate is found in the initial search
			// session query.
			var location = L.latLngBounds(L.latLng(zoom.North, zoom.East), L.latLng(zoom.South, zoom.West));
		} else if (this.locationFound) {
			// pan to the location we found in the search query itself.
			var location = L.latLngBounds(L.latLng(this.location.North, this.location.East),
				L.latLng(this.location.South, this.location.West));
		} else {
			// otherwise, fit the view to encompass all the markers that were drawn
			var location = this.locationFromMarkers;
		}
		this.map.fitBounds(location);
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
		var ss = this.initialSearchSession;
		if (!ss) {
			return;
		}
		var q = ss.getQueryExprOrRef();
		if (q == '') {
			q = 'has:location';
		}
		if (this.mapQuery == null) {
			this.mapQuery = goreact.NewMapQuery(this.props.config.authToken, q, this.handleSearchResults,
				function(){
					this.props.setPendingQuery(false);
				}.bind(this));
			if (this.mapQuery == null) {
				return;
			}
			this.mapQuery.SetLimit(this.QUERY_LIMIT_);
		}
		this.props.setPendingQuery(true);
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
		if (blobs == null) {
			blobs = [];
		}
		// TODO(mpl): instead of all the ifs everywhere, we could just keep on using the
		// cluster as a layer group, but completely disable clustering and spiderifying.
		if (this.clusteringOn) {
			if (this.cluster == null) {
				this.cluster = L.markerClusterGroup({
					// because we handle ourselves below what the visible markers are.
					removeOutsideVisibleBounds: false,
					animate: false,
				});
			}
			this.cluster.addTo(this.map);
			var toAdd = [];
			this.cluster.unfreeze();
		} else {
			if (this.markersGroup == null) {
				this.markersGroup = L.layerGroup();
				this.markersGroup.addTo(this.map);
			}
			var toAdd = L.layerGroup();
		}
		var toKeep = {};
		blobs.forEach(function(b) {
			var br = b.blob;
			var alreadyMarked = this.markers[br]
			if (alreadyMarked && alreadyMarked != null) {
				// marker was already added in the previous zoom level, so do not readd it.
				toKeep[br] = true;
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
			toKeep[br] = true;
			this.markers[br] = marker;
			if (this.clusteringOn) {
				toAdd.push(marker);
			} else {
				toAdd.addLayer(marker);
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
		if (this.clusteringOn) {
			var toRemove = [];
		} else {
			var toRemove = L.layerGroup();
		}
		goog.object.forEach(this.markers, function(mark, br) {
			if (mark == null) {
				return;
			}
			if (!toKeep[br]) {
				this.markers[br] = null;
				if (this.clusteringOn) {
					toRemove.push(mark);
				} else {
					toRemove.addLayer(mark);
				}
			}
		}.bind(this));
		if (this.clusteringOn) {
			this.cluster.removeLayers(toRemove);
			this.cluster.addLayers(toAdd);
			this.cluster.freeze();
		} else {
			this.markersGroup.removeLayer(toRemove);
			this.markersGroup.addLayer(toAdd);
		}

		// TODO(mpl): reintroduce the Around/Continue logic later if needed. For now not
		// needed/useless as MapSorted queries do not support continuation of any kind.

		if (this.firstLoad) {
			this.setCoordinatesFromSearchQuery();
		}
		// even if we're not here because of a zoom change (i.e. either first load, or
		// new search was entered), we still call updateSearchBar here to update the zoom
		// predicate right shift to the search bar.
		this.props.updateSearchBar(this.mapQuery.GetExpr());
	},

	onMapClick: function() {
		this.refreshMapView();
	},

	onZoom: function() {
		if (!this.mapQuery) {
			return;
		}
		if (this.firstLoad) {
			// we are most likely right after the first load, and this is not an intentional
			// pan/zoom, but rather an "automatic" pan/zoom to the first batch of results.
			this.firstLoad = false;
			return;
		}
		if (this.isMoving) {
			clearTimeout(this.zoomTimeout);
		}
		this.isMoving = true;
		this.zoomTimeout = setTimeout(this.onZoomEnd, this.ZOOM_COOLDOWN_);
	},

	onZoomEnd: function() {
		this.isMoving = false;
		if (!this.map) {
			// TODO(mpl): why the hell can this happen?
			return;
		}
		if (!this.mapQuery) {
			return;
		}
		var newBounds = this.map.getBounds();
		this.mapQuery.SetZoom(newBounds.getNorth(), newBounds.getWest(), newBounds.getSouth(), newBounds.getEast());
		this.loadMarkers();
	}
});

cam.MapAspect.getAspect = function(config, availWidth, availHeight, updateSearchBar, setPendingQuery,
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
				updateSearchBar: updateSearchBar,
				setPendingQuery: setPendingQuery,
			});
		},
	};
};
