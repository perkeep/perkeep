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

cam.BlobItemContainerReact = React.createClass({
	displayName: 'BlobItemContainerReact',

	// Margin between items in the layout.
	BLOB_ITEM_MARGIN_: 7,

	// If the last row uses at least this much of the available width before adjustments, we'll call it "close enough" and adjust things so that it fills the entire row. Less than this, and we'll leave the last row unaligned.
	LAST_ROW_CLOSE_ENOUGH_TO_FULL_: 0.85,

	// Distance from the bottom of the page at which we will trigger loading more data.
	INFINITE_SCROLL_THRESHOLD_PX_: 100,

	propTypes: {
		detailURL: React.PropTypes.func.isRequired,  // string->string (blobref->complete detail URL)
		handlers: React.PropTypes.array.isRequired,
		history: React.PropTypes.shape({replaceState:React.PropTypes.func.isRequired}).isRequired,
		onSelectionChange: React.PropTypes.func,
		scrolling: React.PropTypes.shape({
			target:React.PropTypes.shape({addEventListener:React.PropTypes.func.isRequired, removeEventListener:React.PropTypes.func.isRequired}),
			get: React.PropTypes.func.isRequired,
			set: React.PropTypes.func.isRequired,
		}).isRequired,
		searchSession: React.PropTypes.shape({getCurrentResults:React.PropTypes.func.isRequired, addEventListener:React.PropTypes.func.isRequired, loadMoreResults:React.PropTypes.func.isRequired}),
		selection: React.PropTypes.object.isRequired,
		style: React.PropTypes.object,
		thumbnailSize: React.PropTypes.number.isRequired,
		translateY: React.PropTypes.number,
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

		childControls.push(React.DOM.div({
			key: 'marker',
			style: {
				position: 'absolute',
				top: this.layoutHeight_ - 1,
				left: 0,
				height: 1,
				width: 1,
			},
		}));

		// If we haven't filled the window with results, add some more.
		this.fillVisibleAreaWithResults_();

		return React.DOM.div(
			{
				className: 'cam-blobitemcontainer',
				style: cam.object.extend(this.props.style, cam.reactUtil.getVendorProps({
					transform: 'translateY(' + (this.props.translateY || 0) + 'px)',
				})),
				onMouseDown: this.handleMouseDown_,
			},
			childControls
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
			var availWidth = this.props.style.width;
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
		var availThumbWidth = availWidth - (this.BLOB_ITEM_MARGIN_ * (numItems + 1));
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

	isVisible_: function(y) {
		return y >= this.state.scroll && y < (this.state.scroll + this.props.style.height);
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
		this.updateHistoryThrottle_.fire();
		this.setState({scroll:this.props.scrolling.get()});
		this.fillVisibleAreaWithResults_();
	},

	handleChildWheel_: function(child) {
		this.lastWheelItem_ = child.props.blobref;
	},

	// NOTE: This method causes the URL bar to throb for a split second (at least on Chrome), so it should not be called constantly.
	updateHistory_: function() {
		this.props.history.replaceState({scroll:this.props.scrolling.get()});
	},

	fillVisibleAreaWithResults_: function() {
		if (!this.isMounted()) {
			return;
		}

		if ((this.layoutHeight_ - this.state.scroll - this.props.style.height) > this.INFINITE_SCROLL_THRESHOLD_PX_) {
			return;
		}

		this.props.searchSession.loadMoreResults();
	},
});
