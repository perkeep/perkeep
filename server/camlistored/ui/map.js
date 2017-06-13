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

cam.MapAspect = React.createClass({
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
		this.eh_ = new goog.events.EventHandler(this);
	},

	// setCoordinatesFromSearchQuery looks into the search session query for obvious
	// geographic coordinates. Either a location predicate ("loc:seattle"), or a raw
	// query with just coordinates ('raw:{"permanode": {"location": {"west":
	// -123.373922, "north": 48.636011, "east": -121.286750, "south": 46.590087}}}')
	// are considered for now.
	setCoordinatesFromSearchQuery: function() {
		var ss = this.props.searchSession;
		if (!ss) {
			return;
		}
		// TODO(mpl): it is so disgusting that ss.query_ can be both a string or an
		// object. But that's something to fix in search session, with deep repercussions.
		var q = ss.query_;
		if (!q || q == "") {
			return;
		}
		if (!q.permanode || !q.permanode.location) {
			// Not a raw coordinates query.
			if (!goreact.IsLocPredicate(q)) {
				// Not a location type ("loc:foo") query
				return;
			}
			goreact.Geocode(q.substring(goreact.LocPredicatePrefix.length), this.handleCoordinatesFound);
			return;
		}
		this.handleCoordinatesFound({
			North: q.permanode.location.north,
			South: q.permanode.location.south,
			West: q.permanode.location.west,
			East: q.permanode.location.east,
		});
	},

	// handleCoordinatesFound sets this.location (a rectangle), this.latitude, and
	// this.longitude (center of this.location), from the given rectangle.
	handleCoordinatesFound: function(rect) {
		if (!rect) {
			return;
		}

		if (this.sameLocations(rect, this.location)) {
			return;
		}
		this.location = rect;
		// TODO(mpl): I used to need LocationCenter in earlier versions of this code,
		// but not right now. Keeping it for now, as it's still likely we'll need it.
		// Otherwise, remove.
		var center = goreact.LocationCenter(this.location.North, this.location.South, this.location.West, this.location.East);
		this.latitude = center.Lat;
		this.longitude = center.Long;
		this.locationFound = true;
		this.refreshMapView();
		return;
	},

	// refreshMapView pans to the relevant coordinates found for the current search
	// session, if any.
	refreshMapView: function() {
		if (this.locationFound) {
			this.map.fitBounds([[this.location.North, this.location.East], [this.location.South, this.location.West]]);
		}
		// Even after setting the bounds, or the view center+zoom, something is still
		// very wrong, and the map's bounds seem to stay a point (instead of a rectangle).
		// And I can't figure out why. However, any kind of resizing of the window fixes
		// things, hence the following necessary hack.
		window.dispatchEvent(new Event('resize'));
	},

	sameLocations: function(loc1, loc2) {
		return (loc1.North == loc2.North &&
			loc1.South == loc2.South &&
			loc1.West == loc2.West &&
			loc1.East == loc2.East)
	},

	// loadMarkers sets markers on the map for all the permanodes, with a location,
	// found in the current search session. It triggers loading more results from the
	// search session until all of them have been pinned on the map.
	loadMarkers: function() {
		// TODO(mpl): do not load markers if no search (=="main ui page"), so as not to overload?
		var ss = this.props.searchSession;
		if (!ss) {
			return;
		}
		var blobs = ss.getCurrentResults().blobs;
		var lastMarker = null;
		blobs.forEach(function(b) {
			var br = b.blob;
			var marker = this.markers[br];
			if (marker) {
				// marker has already been loaded on the map
				return;
			}
			var m = ss.getResolvedMeta(br);
			if (!m || !m.location) {
				return;
			}
			// TODO(mpl): different marker icons for imgs, tweets, etc.
			// TODO(mpl): we probably want a dir to put all leaflet resources in. later.
			var icon = L.icon({
				// The doc says to set L.Icon.Default.prototype.options.iconUrl, but it does not
				// seem to work. it seems like it sets only the url path of the resource.
				iconUrl: this.props.config.uiRoot + 'marker-icon.png'
			});
			var marker = L.marker([m.location.latitude, m.location.longitude], {icon: icon});
			// Note that we've created that marker already.
			this.markers[br] = marker;
			marker.addTo(this.map);
			if (m.image) {
				// TODO(mpl): Fixed thumbnail size for now, but think of a criterion for thumb size. zoom?
				var img = this.props.config.uiRoot + "thumbnail/" + m.blobRef + "/" + m.file.fileName + "?mh=64";
				marker.bindPopup('<a href="'+this.props.config.uiRoot+br+'"><img src="'+img+'" alt="'+br+'" height="64"></a>');
			} else {
				marker.bindPopup('<a href="'+this.props.config.uiRoot+br+'">'+br+'</a>');
			}
			lastMarker = marker;
		}.bind(this));
		if (lastMarker) {
			// TODO(mpl): do we actually want to popup sometimes?
			// lastMarker.openPopup();
		}
		if (ss.isComplete()) {
			this.refreshMapView();
			return;
		}
		ss.loadMoreResults();
	},

	componentDidMount: function() {
		var map = this.map = L.map(ReactDOM.findDOMNode(this), {
			// TODO(mpl): we probably want to use our own access token. and research how the
			// tiles source (here mapbox) matters.
			layers: [
				L.tileLayer('https://api.tiles.mapbox.com/v4/{id}/{z}/{x}/{y}.png?access_token=pk.eyJ1IjoibWFwYm94IiwiYSI6ImNpejY4NXVycTA2emYycXBndHRqcmZ3N3gifQ.rJcFIG214AriISLbB6B5aw', {
					attribution: 'Map data &copy; <a href="http://openstreetmap.org">OpenStreetMap</a> contributors, ' +
					'<a href="http://creativecommons.org/licenses/by-sa/2.0/">CC-BY-SA</a>, ' +
					'Imagery © <a href="http://mapbox.com">Mapbox</a>',
					id: 'mapbox.streets'
				})
			],
			attributionControl: false,
		});
		map.setView([0., 0.], 3);

		this.loadMarkers();
		map.on('click', this.onMapClick);
		this.eh_.listen(this.props.searchSession, cam.SearchSession.SEARCH_SESSION_CHANGED, function() {
			this.loadMarkers();
		});

		this.setCoordinatesFromSearchQuery();
		// TODO(mpl): alternatively, when the search query is not one we can trivially
		// derive into coordinates , set the coordinates/view from whatever
		// location can be found in the search query results.
	},

	componentWillUnmount: function() {
		this.map.off('click', this.onMapClick);
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

cam.MapAspect.getAspect = function(config, availWidth, availHeight, childSearchSession,
		targetBlobRef, parentSearchSession) {
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
			});
		},
	};
};



