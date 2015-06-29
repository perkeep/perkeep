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

goog.provide('cam.BlobItemContainerReact');

goog.require('goog.array');
goog.require('goog.async.Throttle');
goog.require('goog.dom');
goog.require('goog.events.EventHandler');
goog.require('goog.object');
goog.require('goog.math.Coordinate');
goog.require('goog.math.Size');
goog.require('goog.style');

goog.require('cam.BlobItemReact');
goog.require('cam.SearchSession');
goog.require('cam.SpritedImage');

cam.BlobItemContainerReact = React.createClass({
	displayName: 'BlobItemContainerReact',

	// Margin between items in the layout.
	BLOB_ITEM_MARGIN_: 7,

	// If the last row uses at least this much of the available width before adjustments, we'll call it "close enough" and adjust things so that it fills the entire row. Less than this, and we'll leave the last row unaligned.
	LAST_ROW_CLOSE_ENOUGH_TO_FULL_: 0.85,

	// Distance from the bottom of the page at which we will trigger loading more data.
	INFINITE_SCROLL_THRESHOLD_PX_: 100,

	propTypes: {
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
		searchSession: React.PropTypes.shape({getCurrentResults:React.PropTypes.func.isRequired, addEventListener:React.PropTypes.func.isRequired, loadMoreResults:React.PropTypes.func.isRequired}),
		selection: React.PropTypes.object.isRequired,
		style: React.PropTypes.object,
		thumbnailSize: React.PropTypes.number.isRequired,
	},

	getDefaultProps: function() {
		return {
			style: {},
		};
	},

	componentWillMount: function() {
		this.eh_ = new goog.events.EventHandler(this);
		this.lastCheckedIndex_ = -1;
		this.layoutHeight_ = 0;

		// Minimal information we keep about every single child. We construct the actual child lazily when the user scrolls it into view using handler.
		// @type {Array.<{{position:goog.math.Position, size:goog.math.Size, blobref:string, handler>}
		this.childItems_ = null;

		// TODO(aa): This can be removed when https://code.google.com/p/chromium/issues/detail?id=50298 is fixed and deployed.
		this.updateHistoryThrottle_ = new goog.async.Throttle(this.updateHistory_, 2000);

		// TODO(aa): This can be removed when https://code.google.com/p/chromium/issues/detail?id=312427 is fixed and deployed.
		this.lastWheelItem_ = '';
	},

	componentDidMount: function() {
		this.eh_.listen(this.props.searchSession, cam.SearchSession.SEARCH_SESSION_CHANGED, this.handleSearchSessionChanged_);
		this.eh_.listen(this.props.scrolling.target, 'scroll', this.handleScroll_);
		if (this.props.history.state && this.props.history.state.scroll) {
			this.props.scrolling.set(this.props.history.state.scroll);
		}
		this.fillVisibleAreaWithResults_();
	},

	componentWillReceiveProps: function(nextProps) {
		if (nextProps.searchSession != this.props.searchSession) {
			this.eh_.unlisten(this.props.searchSession, cam.SearchSession.SEARCH_SESSION_CHANGED, this.handleSearchSessionChanged_);
			this.eh_.listen(nextProps.searchSession, cam.SearchSession.SEARCH_SESSION_CHANGED, this.handleSearchSessionChanged_);
			nextProps.searchSession.loadMoreResults();
		}

		this.childItems_ = null;
	},

	componentWillUnmount: function() {
		this.eh_.dispose();
		this.updateHistoryThrottle_.dispose();
	},

	getInitialState: function() {
		return {
			scroll:0,
		};
	},

	render: function() {
		this.updateChildItems_();

		var childControls = this.childItems_.filter(function(item) {
			var visible = this.isVisible_(item.position.y) || this.isVisible_(item.position.y + item.size.height);
			var isLastWheelItem = item.blobref == this.lastWheelItem_;
			return visible || isLastWheelItem;
		}, this).map(function(item) {
			return cam.BlobItemReact({
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

		// If we haven't filled the window with results, add some more.
		this.fillVisibleAreaWithResults_();

		if (childControls.length == 0 && this.props.searchSession.isComplete()) {
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

		var results = this.props.searchSession.getCurrentResults();
		var items = results.blobs.map(function(blob) {
			var blobref = blob.blob;
			var self = this;
			var href = self.props.detailURL(blobref).toString();
			var handler = null;
			this.props.handlers.some(function(h) { return handler = h(blobref, self.props.searchSession, href); });
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

			// Decide how many items are going to be in this row. We choose the number that will result in the smallest adjustment to the image sizes having to be done.
			var rowEnd, rowWidth;

			// For the last item we always use all the rest of the items in this row.
			if (i == lastItem) {
				rowEnd = lastItem;
				rowWidth = nextWidth;
				if (nextWidth / availWidth < this.LAST_ROW_CLOSE_ENOUGH_TO_FULL_) {
					availWidth = nextWidth;
				}

			// If we have at least one item in this row, and the adjustment to the row width is less without the next item than with it, then we leave the next item for the next row.
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

		// Doesn't seem like this should be necessary. Subpixel bug? Aaron can't math?
		var fudge = 1;

		var availThumbWidth = availWidth - (this.BLOB_ITEM_MARGIN_ * (numItems + 1)) - fudge;
		var usedThumbWidth = usedWidth - (this.BLOB_ITEM_MARGIN_ * (numItems + 1));

		for (var i = startIndex; i <= endIndex; i++) {
			// We figure out the amount to adjust each item in this slightly non-intuitive way so that the adjustment is split up as fairly as possible. Figuring out a ratio up front and applying it to all items uniformly can end up with a large amount left over because of rounding.
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
		var piggyWidth = 88;
		var piggyHeight = 62;
		var w = 350;
		var h = 100;

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
			cam.SpritedImage(
				{
					index: 6,
					sheetWidth: 10,
					spriteWidth: piggyWidth,
					spriteHeight: piggyHeight,
					src: 'glitch/npc_piggy__x1_rooked1_png_1354829442.png',
					style: {
						'margin-left': (w - piggyWidth) / 2
					}
				}
			)
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

	handleCheckClick_: function(blobref, e) {
		var blobs = this.props.searchSession.getCurrentResults().blobs;
		var index = goog.array.findIndex(blobs, function(b) { return b.blob == blobref });
		var newSelection = cam.object.extend(this.props.selection, {});

		if (e.shiftKey && this.lastCheckedIndex_ > -1) {
			var low = Math.min(this.lastCheckedIndex_, index);
			var high = Math.max(this.lastCheckedIndex_, index);
			for (var i = low; i <= high; i++) {
				newSelection[blobs[i].blob] = true;
			}
		} else {
			if (newSelection[blobref]) {
				delete newSelection[blobref];
			} else {
				newSelection[blobref] = true;
			}
		}

		this.lastCheckedIndex_ = index;
		this.forceUpdate();

		this.props.onSelectionChange(newSelection);
	},

	handleMouseDown_: function(e) {
		// Prevent the default selection behavior.
		if (e.shiftKey) {
			e.preventDefault();
		}
	},

	handleScroll_: function() {
		this.setState({scroll:this.props.scrolling.get()}, function() {
			this.updateHistoryThrottle_.fire();
			this.fillVisibleAreaWithResults_();
		}.bind(this));
	},

	handleChildWheel_: function(child) {
		this.lastWheelItem_ = child.props.blobref;
	},

	// NOTE: This method causes the URL bar to throb for a split second (at least on Chrome), so it should not be called constantly.
	updateHistory_: function() {
		// second argument (title) is ignored on Firefox, but not optional.
		this.props.history.replaceState(cam.object.extend(this.props.history.state, {scroll:this.state.scroll}), '');
	},

	fillVisibleAreaWithResults_: function() {
		if (!this.isMounted()) {
			return;
		}

		var layoutEnd = this.transformY_(this.layoutHeight_);
		if ((layoutEnd - this.getScrollBottom_()) > this.INFINITE_SCROLL_THRESHOLD_PX_) {
			return;
		}

		this.props.searchSession.loadMoreResults();
	},
});
