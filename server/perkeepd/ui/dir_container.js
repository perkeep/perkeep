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

goog.provide('cam.DirContainer');

goog.require('goog.array');
goog.require('goog.async.Throttle');
goog.require('goog.dom');
goog.require('goog.events.EventHandler');
goog.require('goog.object');
goog.require('goog.math.Coordinate');
goog.require('goog.math.Size');
goog.require('goog.style');

goog.require('cam.BlobItemReact');
goog.require('cam.SpritedImage');
goog.require('cam.ServerConnection');

// DirChildrenSession provides a Query object suitable to send search queries
// to get the children of a directory.
cam.DirChildrenSession = function(
	serverConnection,
	parentDir,
	limit,
	updateSearchSession,
	triggerRender
) {
	this.serverConnection_ = serverConnection;

	// Directory the query is about
	this.parentDir_ = parentDir;

	// Maximum number of search results that should be returned
	this.limit_ = limit;

	// Blob around which the returned results should be centered. It
	// implies that the search results can be sorted.
	this.around_ = null;

	// Provided by the caller, to update its search session,
	// with the provided set of results, in JSON form.
	this.updateSearchSession_ = updateSearchSession;

	// Provided by the caller, to start rerendering the DOM, after
	// the query's results have been merged with the current set of
	// results, and the caller's search session has been updated with
	// that set.
	this.triggerRender_ = triggerRender;

	// Currently known set of descendants of ParentDir. Subsequent new
	// query results, i.e. with a movind Around parameter, are merged
	// with Blobs.
	this.blobs_ = [];

	// Meta is the map of descriptions for the blobs.
	this.meta_ = {};

	// Makes sure there's only ever one query at most in flight.
	this.pending_ = false;

	// Wether we've already gotten all the descendants of ParentDir.
	this.isComplete_ = false;
};

cam.DirChildrenSession.prototype.isComplete = function() {
	return this.isComplete_;
};

cam.DirChildrenSession.prototype.get = function() {
	if (this.isComplete_) {
		return;
	}
	if (this.pending_) {
		return;
	}
	this.pending_ = true;

	const query = {
		"logical": {
			"op": "or",
			"a": {
				"file": {
					"parentDir": {
						"blobRefPrefix": this.parentDir_,
					},
				},
			},
			"b": {
				"dir": {
					"parentDir": {
						"blobRefPrefix": this.parentDir_,
					},
				},
			},
		},
	};
	const opts = {
		"limit": this.limit_,
		"sort": "blobref",
		"describe": {
			"rules": [
				{
					"attrs": ["camliContent", "camliContentImage"],
				},
			],
		},
		"around": this.around_,
	};

	this.serverConnection_.search(
		query,
		opts,
		function(results){
			this.mergeResults_(results);
			const newResults = this.results_();
			this.updateSearchSession_(newResults);
			this.triggerRender_();
			this.pending_ = false;
		}.bind(this),
	);
};

cam.DirChildrenSession.prototype.mergeResults_ = function(results) {
	if (this.isComplete_) {
		return;
	}
	if (!results || !results.blobs || results.blobs.length === 0) {
		return;
	}
	if (!results.description || !results.description.meta) {
		return;
	}

	const requestedAround = this.around_;
	if (this.blobs_.length === 0) {
		// first batch
		this.blobs_ = results.blobs;
		this.meta_ = results.description.meta;
		this.around_ = this.blobs_[this.blobs_.length-1].blob;
		return;
	}
	const lastInResults = results.blobs[results.blobs.length-1].blob;

	let found = false;
	let afterAroundIdx = 0;
	// Look for merging point.
	// First jump to the middle of the results.Blobs and see if that's
	// Around. If not, do slow search.
	const middle = Math.floor(results.blobs.length / 2);
	if (results.blobs[middle].blob === requestedAround) {
		// odd case
		found = true;
		afterAroundIdx = middle + 1;
	} else if (results.blobs[middle+1].blob === requestedAround) {
		// even case
		found = true;
		afterAroundIdx = middle + 2;
	} else {
		// slow search
		for(let i = results.blobs.length-1; i>=0; i--) {
			if (results.blobs[i].blob !== requestedAround) {
				continue;
			}
			afterAroundIdx = i + 1;
			found = true;
			break;
		}
	}
	if (!found) {
		return;
	}

	if (requestedAround === lastInResults) {
		// we don't have to worry about out of order batches, because we only "increment"
		// this.around_ when we've received the previously requested one.
		this.isComplete_ = true;
	}
	// Reject "stale" results. They should never occur though, since we
	// supress with this.pending_, and we request everything in order.
	if (this.meta_[lastInResults]) {
		return;
	}

	this.blobs_.push(...results.blobs.slice(afterAroundIdx));
	for (const [key, value] of Object.entries(results.description.meta)) {
		this.meta_[key] = value;
	}
	this.around_ = this.blobs_[this.blobs_.length-1].blob;
};

cam.DirChildrenSession.prototype.results_ = function(){
	const res = {
		blobs: this.blobs_,
		description: {
			meta: this.meta_,
		},
	};
	return res;
};


// FakeSearchSession provides just enough of a common interface with
// cam.SearchSession to satisfy the needs of the objects within the container
// (this.props.handlers). It does not actually do any searching; it is populated
// with the results found with DirChildrenSession.
cam.FakeSearchSession = function(data) {
	this.isComplete_ = false;
	this.resetData_();
};

// Returns all the data we currently have loaded.
// It is guaranteed to return the following properties:
// blobs // non-null
// description
// description.meta
cam.FakeSearchSession.prototype.getCurrentResults = function() {
	return this.data_;
};

// Returns true if it is known that all data which can be loaded for this query has been.
cam.FakeSearchSession.prototype.isComplete = function() {
	return this.isComplete_;
}

cam.FakeSearchSession.prototype.getMeta = function(blobref) {
	return this.data_.description.meta[blobref];
};

cam.FakeSearchSession.prototype.getResolvedMeta = function(blobref) {
	var meta = this.data_.description.meta[blobref];
	if (meta && meta.camliType == 'permanode') {
		var camliContent = cam.permanodeUtils.getSingleAttr(meta.permanode, 'camliContent');
		if (camliContent) {
			return this.data_.description.meta[camliContent];
		}
	}
	return meta;
};

cam.FakeSearchSession.prototype.getTitle = function(blobref) {
	var meta = this.getMeta(blobref);
	if (meta.camliType == 'permanode') {
		var title = cam.permanodeUtils.getSingleAttr(meta.permanode, 'title');
		if (title) {
			return title;
		}
	}
	var rm = this.getResolvedMeta(blobref);
	return (rm && rm.camliType == 'file' && rm.file.fileName) || (rm && rm.camliType == 'directory' && rm.dir.fileName) || '';
};

cam.FakeSearchSession.prototype.resetData_ = function() {
	this.data_ = {
		blobs: [],
		description: {
			meta: {}
		}
	};
};

cam.FakeSearchSession.prototype.populate = function(result) {
	if (!result) {
		result = {};
	}
	if (!result.blobs) {
		result.blobs = [];
	}
	if (!result.description) {
		result.description = {};
	}
	this.data_.blobs = result.blobs;
	this.data_.description.meta = result.description.meta;
};

cam.FakeSearchSession.prototype.setComplete = function(isComplete) {
	this.isComplete_ = isComplete;
};

// DirContainer is like BlobItemContainerReact, except for a directory, instead
// of a permanode with (camliMember) children. So each child object it contains and
// displays directly represents a file or directory, not necessarily anchored with
// a permanode.
cam.DirContainer = React.createClass({
	displayName: 'DirContainer',

	// Margin between items in the layout.
	BLOB_ITEM_MARGIN_: 7,

	// If the last row uses at least this much of the available width before
	// adjustments, we'll call it "close enough" and adjust things so that it fills the
	// entire row. Less than this, and we'll leave the last row unaligned.
	LAST_ROW_CLOSE_ENOUGH_TO_FULL_: 0.85,

	// Distance from the bottom of the page at which we will trigger loading more data.
	INFINITE_SCROLL_THRESHOLD_PX_: 100,

	QUERY_LIMIT_: 50,

	propTypes: {
		config: React.PropTypes.object.isRequired,
		blobRef: React.PropTypes.string.isRequired,
		availHeight: React.PropTypes.number.isRequired,
		availWidth: React.PropTypes.number.isRequired,
		detailURL: React.PropTypes.func.isRequired,  // string->string (blobref->complete detail URL)
		handlers: React.PropTypes.array.isRequired,
		history: React.PropTypes.shape({replaceState:React.PropTypes.func.isRequired}).isRequired,
		onSelectionChange: React.PropTypes.func,
		scale: React.PropTypes.number.isRequired,
		scaleEnabled: React.PropTypes.bool.isRequired,
		scrolling: React.PropTypes.shape({
			target:React.PropTypes.shape({addEventListener:React.PropTypes.func.isRequired, removeEventListener:React.PropTypes.func.isRequired}),
			get: React.PropTypes.func.isRequired,
			set: React.PropTypes.func.isRequired,
		}).isRequired,
		selection: React.PropTypes.object.isRequired,
		style: React.PropTypes.object,
		thumbnailSize: React.PropTypes.number.isRequired,
		serverConnection: React.PropTypes.instanceOf(cam.ServerConnection).isRequired,
	},

	getDefaultProps: function() {
		return {
			style: {},
		};
	},

	componentWillMount: function() {
		this.eh_ = new goog.events.EventHandler(this);
		this.results = {
			blobs: [],
			description: {
				meta: {}
			}
		};
		this.childItems_ = null;
		this.setupSearchSession_();
		this.lastCheckedIndex_ = -1;
		this.layoutHeight_ = 0;
		// Minimal information we keep about every single child. We construct the actual
		// child lazily when the user scrolls it into view using handler.
		// @type {Array.<{{position:goog.math.Position, size:goog.math.Size, blobref:string, handler>}
		this.lastWheelItem_ = '';
	},

	setupSearchSession_: function() {
		this.searchSession = new cam.FakeSearchSession();
		this.DirChildrenSession = new cam.DirChildrenSession(
			this.props.serverConnection,
			this.props.blobRef,
			this.QUERY_LIMIT_,
			function(sr) {
				if (!sr || !sr.blobs || sr.blobs.length === 0) {
					return;
				}
				this.searchSession.populate(sr);
				this.searchSession.setComplete(this.DirChildrenSession.isComplete());
			}.bind(this),
			function() {
				this.handleSearchSessionChanged_();
			}.bind(this),
		);
	},

	componentDidMount: function() {
		this.eh_.listen(this.props.scrolling.target, 'scroll', this.handleScroll_);
		if (this.props.history.state && this.props.history.state.scroll) {
			this.props.scrolling.set(this.props.history.state.scroll);
		}
		this.fillVisibleAreaWithResults_();
	},

	componentWillReceiveProps: function(nextProps) {
		this.childItems_ = null;
	},

	componentWillUnmount: function() {
		this.eh_.dispose();
	},

	getInitialState: function() {
		return {
			scroll:0,
		};
	},

	render: function() {
		this.updateChildItems_();

		// TODO(mpl): make (some of the) controls work on non-permanodes.

		var childControls = this.childItems_.filter(function(item) {
			var visible = this.isVisible_(item.position.y) || this.isVisible_(item.position.y + item.size.height);
			var isLastWheelItem = item.blobref == this.lastWheelItem_;
			return visible || isLastWheelItem;
		}, this).map(function(item) {
			return React.createElement(cam.BlobItemReact, {
					key: item.blobref,
					blobref: item.blobref,
					checked: Boolean(this.props.selection[item.blobref]),
					onCheckClick: this.props.onSelectionChange ? this.handleCheckClick_ : null,
					onWheel: this.handleChildWheel_,
					position: item.position,
				},
				item.handler.createContent(item.size)
			);
		}, this);

		this.fillVisibleAreaWithResults_();

		if (childControls.length == 0 && this.searchSession.isComplete()) {
			childControls.push(this.getNoResultsMessage_());
		}

		var transformStyle = {};
		var scale = this.props.scaleEnabled ? this.props.scale : 1;
		transformStyle[cam.reactUtil.getVendorProp('transform')] = goog.string.subs('scale3d(%s, %s, 1)', scale, scale);
		transformStyle[cam.reactUtil.getVendorProp('transformOrigin')] = goog.string.subs('left %spx 0', this.state.scroll);

		return React.DOM.div(
			{
				className: 'cam-blobitemcontainer',
				style: cam.object.extend(this.props.style, {
					height: this.layoutHeight_,
					width: this.props.availWidth,
				}),
				onMouseDown: this.handleMouseDown_,
			},
			React.DOM.div(
				{
					className: 'cam-blobitemcontainer-transform',
					style: transformStyle,
				},
				childControls
			)
		);
	},

	updateChildItems_: function() {
		if (this.childItems_ !== null) {
			return;
		}

		this.childItems_ = [];

		var results = this.searchSession.getCurrentResults();
		if (!results || !results.blobs) {
			return;
		}
		var items = results.blobs.map(function(blob) {
			var blobref = blob.blob;
			var self = this;
			var href = self.props.detailURL(blobref).toString();
			var handler = null;
			this.props.handlers.some(function(h) { return handler = h(blobref, self.searchSession, href); });
			return {
				blobref: blobref,
				handler: handler,
				position: null,
				size: null,
			};
		}.bind(this));

		var currentTop = this.BLOB_ITEM_MARGIN_;
		var currentWidth = this.BLOB_ITEM_MARGIN_;
		var rowStart = 0;
		var lastItem = results.blobs.length - 1;

		for (var i = rowStart; i <= lastItem; i++) {
			var item = items[i];
			var availWidth = this.props.availWidth;
			var nextWidth = currentWidth + this.props.thumbnailSize * item.handler.getAspectRatio() + this.BLOB_ITEM_MARGIN_;
			if (i != lastItem && nextWidth < availWidth) {
				currentWidth = nextWidth;
				continue;
			}

			// Decide how many items are going to be in this row. We choose the number that
			// will result in the smallest adjustment to the image sizes having to be done.
			var rowEnd, rowWidth;

			// For the last item we always use all the rest of the items in this row.
			if (i == lastItem) {
				rowEnd = lastItem;
				rowWidth = nextWidth;
				if (nextWidth / availWidth < this.LAST_ROW_CLOSE_ENOUGH_TO_FULL_) {
					availWidth = nextWidth;
				}

			// If we have at least one item in this row, and the adjustment to the row width
			// is less without the next item than with it, then we leave the next item for the
			// next row.
			} else if (i > rowStart && (availWidth - currentWidth <= nextWidth - availWidth)) {
				rowEnd = i - 1;
				rowWidth = currentWidth;

			// Otherwise we include the next item in this row.
			} else {
				rowEnd = i;
				rowWidth = nextWidth;
			}

			currentTop += this.updateChildItemsRow_(items, rowStart, rowEnd, availWidth, rowWidth, currentTop) + this.BLOB_ITEM_MARGIN_;

			currentWidth = this.BLOB_ITEM_MARGIN_;
			rowStart = rowEnd + 1;
			i = rowEnd;
		}

		this.layoutHeight_ = currentTop;
	},

	updateChildItemsRow_: function(items, startIndex, endIndex, availWidth, usedWidth, top) {
		var currentLeft = 0;
		var rowHeight = Number.POSITIVE_INFINITY;

		var numItems = endIndex - startIndex + 1;

		var fudge = 1;
		var availThumbWidth = availWidth - (this.BLOB_ITEM_MARGIN_ * (numItems + 1)) - fudge;
		var usedThumbWidth = usedWidth - (this.BLOB_ITEM_MARGIN_ * (numItems + 1));

		for (var i = startIndex; i <= endIndex; i++) {
			// We figure out the amount to adjust each item in this slightly non-intuitive
			// way so that the adjustment is split up as fairly as possible. Figuring out a
			// ratio up front and applying it to all items uniformly can end up with a large
			// amount left over because of rounding.
			var item = items[i];
			var numItemsLeft = (endIndex + 1) - i;
			var delta = Math.round((availThumbWidth - usedThumbWidth) / numItemsLeft);
			var originalWidth = this.props.thumbnailSize * item.handler.getAspectRatio();
			var width = originalWidth + delta;
			var ratio = width / originalWidth;
			var height = Math.round(this.props.thumbnailSize * ratio);

			item.position = new goog.math.Coordinate(currentLeft + this.BLOB_ITEM_MARGIN_, top);
			item.size = new goog.math.Size(width, height);
			this.childItems_.push(item);

			currentLeft += width + this.BLOB_ITEM_MARGIN_;
			usedThumbWidth += delta;
			rowHeight = Math.min(rowHeight, height);
		}

		for (var i = startIndex; i <= endIndex; i++) {
			this.childItems_[i].size.height = rowHeight;
		}

		return rowHeight;
	},

	getNoResultsMessage_: function() {
		var keepyWidth = 118;
		var keepyHeight = 108;
		var w = 200;
		var h = 200;

		return React.DOM.div(
			{
				key: 'no-results',
				className: 'cam-blobitemcontainer-no-results',
				style: {
					width: w,
					height: h,
					left: (this.props.availWidth - w) / 2,
					top: (this.props.availHeight - h) / 3
				},
			},
			React.DOM.div(null, 'No results found'),
			React.createElement(cam.SpritedImage, {
				index: 0,
				sheetWidth: 1,
				spriteWidth: keepyWidth,
				spriteHeight: keepyHeight,
				src: 'keepy/keepy-sad.png',
				style: {
					marginLeft: (w - keepyWidth) / 2
				}
			})
		);
	},

	getScrollFraction_: function() {
		var max = this.layoutHeight_;
		if (max == 0)
			return 0;
		return this.state.scroll / max;
	},

	getTranslation_: function() {
		var maxOffset = (1 - this.props.scale) * this.layoutHeight_;
		var currentOffset = maxOffset * this.getScrollFraction_();
		return currentOffset;
	},

	transformY_: function(y) {
		return y * this.props.scale + this.getTranslation_();
	},

	getScrollBottom_: function() {
		return this.state.scroll + this.props.availHeight;
	},

	isVisible_: function(y) {
		y = this.transformY_(y);
		return y >= this.state.scroll && y < this.getScrollBottom_();
	},

	handleSearchSessionChanged_: function() {
		this.childItems_ = null;
		this.forceUpdate();
	},

	handleMouseDown_: function(e) {
		// Prevent the default selection behavior.
		if (e.shiftKey) {
			e.preventDefault();
		}
	},

	handleScroll_: function() {
		this.setState({scroll:this.props.scrolling.get()}, function() {
			this.fillVisibleAreaWithResults_();
		}.bind(this));
	},

	handleChildWheel_: function(child) {
		this.lastWheelItem_ = child.props.blobref;
	},

	fillVisibleAreaWithResults_: function() {
		var layoutEnd = this.transformY_(this.layoutHeight_);
		if ((layoutEnd - this.getScrollBottom_()) > this.INFINITE_SCROLL_THRESHOLD_PX_) {
			// When we've loaded enough items that the last line of them is 100 px below
			// the bottom of the screen.
			return;
		}
		if (!this.DirChildrenSession) {
			return;
		}
		this.DirChildrenSession.get();
	},

});
