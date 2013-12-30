/*
Copyright 2013 Google Inc.

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

goog.require('camlistore.AnimationLoop');
goog.require('camlistore.ServerConnection');

goog.require('goog.math.Size');

function cached(fn) {
	var lastProps;
	var lastState;
	var lastVal;
	return function() {
		if (lastState == this.state && lastProps == this.props) {
			return lastVal;
		}
		lastProps = this.props;
		lastState = this.state;
		lastVal = fn.apply(this, arguments);
		return lastVal;
	}
}

var DetailView = React.createClass({
	PREVIEW_MARGIN: 20,

	componentDidMount: function(root) {
		var imageSize = 100;  // We won't use this exact value; we only care about the aspect ratio.
		var connection = new camlistore.ServerConnection(this.props.config);
		connection.describeWithThumbnails(this.props.blobref, imageSize, function(description) {
			this.setState({
				description: description
			});
		}.bind(this));
	},

	getPreviewSize_: cached(function() {
		var meta = this.getPermanodeMeta_();
		if (!meta) {
			return;
		}

		var aspect = new goog.math.Size(meta.thumbnailWidth, meta.thumbnailHeight);
		var available = new goog.math.Size(
			this.props.width - this.getSidebarWidth_() - this.PREVIEW_MARGIN * 2,
			this.props.height - this.PREVIEW_MARGIN * 2);
		return aspect.scaleToFit(available);
	}),

	render: function() {
		var description = this.state ? this.state.description : '';
		return (
			React.DOM.div({className:'detail-view', style: this.getStyle_()},
				React.DOM.img({className:'detail-view-preview', key:'preview', src: this.getSrc_(), style: this.getPreviewStyle_()}),
				React.DOM.div({className:'detail-view-sidebar', key:'sidebar', style: this.getSidebarStyle_()},
					React.DOM.pre({key:'sidebar-pre'}, JSON.stringify(description, null, 2)))));
	},

	getSrc_: function() {
		if (!this.state) {
			// TODO(aa): Loading animation
			return '';
		}

		var previewSize = this.getPreviewSize_();
		// Only re-request the image if we're increasing in size. Otherwise, let the browser resample.
		if (previewSize.height < (this.lastImageHeight || 0)) {
			console.log('Not re-requesting image becasue new size is smaller than existing...');
		} else {
			// If we re-request, ask for one bigger than we need right now, so that we're not constantly re-requesting as the browser resizes.
			this.lastImageHeight = previewSize.height * 1.25;
			console.log('Requesting new image with size: ' + this.lastImageHeight);
		}

		var uri = new goog.Uri(this.getPermanodeMeta_().thumbnailSrc);
		uri.setParameterValue('mh', this.lastImageHeight);
		return uri.toString();
	},

	getStyle_: function() {
		return {
			width: this.props.width,
			height: this.props.height
		}
	},

	getPreviewStyle_: function() {
		if (!this.state || !this.getPreviewSize_().height) {
			return {
				visibility: 'hidden'
			}
		}

		var avail = new goog.math.Size(this.props.width - this.getSidebarWidth_(), this.props.height);
		return {
			top: (avail.height - this.getPreviewSize_().height) / 2,
			left: (avail.width - this.getPreviewSize_().width) / 2,
			width: this.getPreviewSize_().width,
			height: this.getPreviewSize_().height
		}
	},

	getSidebarStyle_: function() {
		return {
			width: this.getSidebarWidth_()
		}
	},

	getSidebarWidth_: function() {
		return Math.max(this.props.width * 0.2, 300);
	},

	getPermanodeMeta_: function() {
		if (!this.state) {
			return null;
		}
		return this.state.description.meta[this.props.blobref];
	}
});
