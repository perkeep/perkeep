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

goog.provide('cam.CacheBusterIframe');

goog.require('goog.Uri');

goog.require('cam.Navigator');

// Reload/shift-reload doesn't actually reload iframes from server in Chrome.
// We should implement content stamping, but for now, this is a workaround.
cam.CacheBusterIframe = React.createClass({
	propTypes: {
		baseURL: React.PropTypes.instanceOf(goog.Uri).isRequired,
		height: React.PropTypes.number.isRequired,
		onChildFrameClick: React.PropTypes.func,
		src: React.PropTypes.instanceOf(goog.Uri).isRequired,
		width: React.PropTypes.number.isRequired,
	},

	componentDidMount: function() {
		this.getDOMNode().contentWindow.addEventListener('DOMContentLoaded', this.handleDOMContentLoaded_);
	},

	componentDidUpdate: function() {
		this.componentDidMount();
	},

	getInitialState: function() {
		return {
			height: this.props.height,
			r: Date.now(),
		}
	},

	render: function() {
		var uri = this.props.src.clone();
		uri.setParameterValue('r', this.state.r);
		return React.DOM.iframe({
			height: this.state.height,
			src: uri.toString(),
			style: {
				border: 'none',
			},
			width: this.props.width,
		});
	},

	handleDOMContentLoaded_: function() {
		this.updateSize_();
		if (this.props.onChildFrameClick) {
			this.getDOMNode().contentWindow.addEventListener('click', this.handleChildFrameClick_);
		}
	},

	handleChildFrameClick_: function(e) {
		var elm = cam.Navigator.shouldHandleClick(e);
		if (!elm) {
			return;
		}

		var oldURL = new goog.Uri(e.target.href);
		var newURL = this.props.baseURL.clone();
		var query = oldURL.getParameterValue('q');

		if (query) {
			newURL.setParameterValue('q', query);
		} else {
			newURL.setPath(newURL.getPath() + (oldURL.getParameterValue('p') || oldURL.getParameterValue('d') || oldURL.getParameterValue('b')));
		}

		try {
			if (this.props.onChildFrameClick(newURL)) {
				e.preventDefault();
			}
		} catch (ex) {
			e.preventDefault();
			throw ex;
		}
	},

	updateSize_: function() {
		if (!this.isMounted()) {
			return;
		}

		var node = this.getDOMNode();
		if (node && node.contentDocument && node.contentDocument.body) {
			node.contentDocument.body.style.overflowY = 'hidden';
			this.setState({height: node.contentDocument.documentElement.offsetHeight });
		}
		window.setTimeout(this.updateSize_, 200);
	},
});
