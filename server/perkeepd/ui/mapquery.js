/*
Copyright 2021 The Perkeep Authors

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

goog.provide('cam.MapUtils');
goog.provide('cam.MapQuery');


// hasZoomParameter returns whether queryString is the "q" parameter of
// a search query, and whether that parameter contains a map zoom (map predicate).
cam.MapUtils.hasZoomParameter = function(queryString) {
	let qs = queryString.trim();
	if (!qs.startsWith("q=")) {
		return false;
	}
	qs = qs.slice("q=".length);
	const fields = qs.split(/\s+/);
	for (const field of fields) {
		if (cam.MapUtils.isLocMapPredicate(field)) {
			return true;
		}
	}
	return false;
};


// isLocMapPredicate returns whether predicate is a map location predicate.
cam.MapUtils.isLocMapPredicate = function(predicate) {
	const rectangle = cam.MapUtils.rectangleFromPredicate(predicate, "map");
	return rectangle !== null;
};


// rectangleFromPredicate, if predicate is a valid location predicate of the given kind
// and returns the corresponding rectangular area.
cam.MapUtils.rectangleFromPredicate = function(predicate, kind) {
	if (!predicate.startsWith(`${kind}:`)) {
		return null;
	}
	const loc = predicate.slice(`${kind}:`.length);
	const coords = loc.split(",");
	if (coords.length !== 4) {
		return null;
	}
	const coordsFloats = [];
	for (const coord of coords) {
		const f = parseFloat(coord);
		if (isNaN(f)) {
			return null;
		}
		coordsFloats.push(f);
	}
	return {
		"north": coordsFloats[0],
		"south": coordsFloats[1],
		"west": coordsFloats[2],
		"east": coordsFloats[3],
	};
};


// shiftZoomPredicate looks for a "map:" predicate in expr, and if found, moves
// it at the end of the expression if necessary.
cam.MapUtils.shiftZoomPredicate = function(expr) {
	return cam.MapUtils.handleZoomPredicate(expr, false, "");
};


// deleteZoomPredicate looks for a "map:" predicate in expr, and if found,
// removes it.
cam.MapUtils.deleteZoomPredicate = function(expr) {
	return cam.MapUtils.handleZoomPredicate(expr, true, "");
};


cam.MapUtils.handleZoomPredicate = function(expr, del, replacement) {
	if (del && replacement !== "") {
		throw "deletion mode and replacement mode are mutually exclusive";
	}

	let replace = false;
	if (replacement !== "") {
		replace = true;
	}

	const sq = expr.trim();
	if (sq === "") {
		return expr;
	}
	const fields = sq.split(/\s+/);
	let pos = -1;
	for(const [k, v] of fields.entries()) {
		if (cam.MapUtils.isLocMapPredicate(v)) {
			pos = k;
			break;
		}
	}

	// easiest case: there is no zoom
	if (pos === -1) {
		if (replace) {
			return `${sq} ${replacement}`;
		}
		return sq;
	}

	// there's already a zoom at the end
	if (pos === fields.length - 1) {
		if (del) {
			return fields.slice(0, pos).join(" ");
		}
		if (replace) {
			return fields.slice(0, pos).join(" ") + " " + replacement;
		}
		return sq;
	}

	// There's a zoom somewhere else in the expression

	// does it have a preceding "and"?
	let before = 0;
	if (pos > 0 && fields[pos-1] === "and") {
		before = pos - 1;
	} else {
		before = pos;
	}
	// does it have a following "and"?
	let after = 0;
	if (pos < fields.length-1 && fields[pos+1] === "and") {
		after = pos + 2;
	} else {
		after = pos + 1;
	}
	// erase potential "and"s, and shift the zoom to the end of the expression
	if (del) {
		return fields.slice(0, before).join(" ") + " " + fields.slice(after).join(" ");
	}
	if (replace) {
		return fields.slice(0, before).join(" ") + " " + fields.slice(after).join(" ") + " " + replacement;
	}

	return fields.slice(0, before).join(" ") + " " + fields.slice(after).join(" ") + " " + fields[pos];
};


// checkZoomExpr verifies that expr does not violate the rules about the map
// predicate, which are:
// 1) only one map predicate per expression
// 2) since it is interpreted as a logical "and" to the rest of the expression,
// logical "or"s around it are forbidden.
// To be complete we should strip any potential parens around the map
// predicate itself. But if we start concerning ourselves with such details, we
// should switch to using a proper parser, like it is done server-side.
cam.MapUtils.checkZoomExpr = function(expr) {
	const sq = expr.trim();
	if (sq === "") {
		return null;
	}
	const fields = sq.split(/\s+/);
	if (fields.length === 1) {
		return null;
	}
	const pos = [];
	for (const [k, v] of fields.entries()) {
		if (cam.MapUtils.isLocMapPredicate(v)) {
			pos.push(k);
		}
	}
	// Did we find several "map:" predicates?
	if (pos.length > 1) {
		return "map predicate should be unique. See https://camlistore.org/doc/search-ui";
	}
	for (const v of pos) {
		// does it have an "or" following?
		if (v < fields.length-1 && fields[v+1] === "or") {
			return 'map predicate with logical "or" forbidden. See https://camlistore.org/doc/search-ui';
		}
		// does it have a preceding "or"?
		if (v > 0 && fields[v-1] === "or") {
			return 'map predicate with logical "or" forbidden. See https://camlistore.org/doc/search-ui';
		}
	}
	return null;
};


cam.MapUtils.NewMapQuery = function(
	serverConnection,
	expr,
	callback,
	cleanup,
) {
	const error = cam.MapUtils.checkZoomExpr(expr);
	if (error) {
		alert(error);
		return null;
	}
	expr = cam.MapUtils.shiftZoomPredicate(expr);
	return new cam.MapQuery(
		serverConnection,
		expr,
		callback,
		cleanup,
		50,
	);
};


cam.MapUtils.mapToLocrect = function(expr) {
	const sq = expr.trim();
	if (sq === "") {
		return expr;
	}
	const fields = sq.split(/\s+/);
	const lastPred = fields[fields.length-1];
	if (cam.MapUtils.isLocMapPredicate(lastPred)) {
		const locrect = lastPred.replace("map:", "locrect:");
		if (fields.length === 1) {
			return locrect;
		}
		return `(${fields.slice(0, fields.length-1).join(" ")}) ${locrect}`;
	}
	return expr;
};


// wrapTo180 returns longitude converted to the [-180,180] interval.
cam.MapUtils.wrapTo180 = function(longitude) {
	if (longitude >= -180 && longitude <= 180) {
		return longitude;
	}
	if (longitude == 0) {
		return longitude;
	}
	if (longitude > 0) {
		return (longitude+180 % 360) - 180;
	}
	return (longitude-180 % 360) + 180;
};


cam.MapQuery = function(
	serverConnection,
	expr,
	callback,
	cleanup,
	limit,
) {
	// serverConnection is the server connection.
	this.serverConnection_ = serverConnection;

	// expr is the search query expression.
	this.expr_ = expr;

	// callback is the function to run on the JSON-ified serach results, if the
	// search was successful.
	this.callback_ = callback;

	// cleanup is run once, right before callback, or on any error that occurs before
	// callback.
	this.cleanup_ = cleanup;

	// limit is the maximum number of search results that should be returned.
	this.limit_ = limit;

	// pending makes sure there's only ever one query at most in flight.
	this.pending_ = false;

	// nextZoom is the location area that is requested for the next query.
	this.nextZoom_ = null;

	// zoom is the location area that was requested for the last successfull query.
	this.zoom_ = null;
};


cam.MapQuery.prototype.getExpr = function() {
	return this.expr_;
};

cam.MapQuery.prototype.setLimit = function(limit) {
	this.limit_ = limit;
};

// setZoom modifies the query's search expression: it uses the given coordinates
// in a map predicate to constrain the search expression to the defined area,
// effectively acting like a map zoom.
//
// The map predicate is defined like locrect, and it has a similar meaning.
// However, it is not defined server-side, and it is specifically meant to
// represent the area of the world that is visible in the screen when using the map
// aspect, and in particular when zooming or panning. As such, it follows stricter
// rules than the other predicates, which are:
//
// 1. only one map predicate is allowed in the whole expression.
// 2. since the map predicate is interpreted as if it were a logical 'and' with
// the rest of the whole expression (regardless of its position within the
// expression), logical 'or's around it are forbidden.
//
// The map predicate is also moved to the end of the expression, for clarity
cam.MapQuery.prototype.setZoom = function(north, west, south, east) {
	if (west <= east && east-west > 360) {
		// we're just zoomed out very far.
		west = -179.99;
		east = 179.99;
	}
	const precision = 1e-6;
	// since we print the locrect at a given arbitrary precision (e-6), we need to
	// round everything "up", to make sure we don't exclude points on the boundaries.
	const newNorth = north + precision;
	const newSouth = south - precision;
	const newWest = cam.MapUtils.wrapTo180(west - precision);
	const newEast = cam.MapUtils.wrapTo180(east + precision);

	this.nextZoom_ = {
		"north": newNorth,
		"south": newSouth,
		"west": newWest,
		"east": newEast,
	};

	const zoomExpr = `map:${newNorth.toFixed(6)},${newWest.toFixed(6)},${newSouth.toFixed(6)},${newEast.toFixed(6)}`;

	this.expr_ = cam.MapUtils.handleZoomPredicate(this.expr_, false, zoomExpr);
}


cam.MapQuery.prototype.send = function() {
	if (this.pending_) {
		this.cleanup_();
		return;
	}
	this.pending = true;

	this.expr_ = cam.MapUtils.shiftZoomPredicate(this.expr_);
	const expr = cam.MapUtils.mapToLocrect(this.expr_);

	const query = expr;
	const opts = {
		"limit": this.limit_,
		"sort": "map",
		"describe": {
			"depth": 1,
			"rules": [
				{
					"attrs": ["camliContent", "camliContentImage"]
				},
				{
					"ifCamliNodeType": "foursquare.com:checkin",
					"attrs": ["foursquareVenuePermanode"]
				},
				{
					"ifCamliNodeType": "foursquare.com:venue",
					"attrs": ["camliPath:photos"],
					"rules": [
						{
							"attrs": ["camliPath:*"],
						},
					],
				}
			],
		},
	};

	this.serverConnection_.search(
		query,
		opts,
		function(results){
			this.zoom_ = this.nextZoom_;
			this.pending_ = false;
			this.cleanup_();
			this.callback_(results);
		}.bind(this),
	);

};
