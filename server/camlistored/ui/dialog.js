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

goog.provide('cam.Dialog');

cam.Dialog = React.createClass({
	propTypes: {
		availWidth: React.PropTypes.number.isRequired,
		availHeight: React.PropTypes.number.isRequired,
		width: React.PropTypes.number.isRequired,
		height: React.PropTypes.number.isRequired,
		borderWidth: React.PropTypes.number.isRequired,
		onClose: React.PropTypes.func,
	},

	render: function() {
		return React.DOM.div(
			{
				className: 'cam-dialog-mask',
			},
			React.DOM.div(
				{
					className: 'cam-dialog',
					style: {
						'width': this.props.width,
						'height': this.props.height,
						'left': (this.props.availWidth - this.props.width) / 2,
						'top': (this.props.availHeight - this.props.height) / 2,
						'border-width': this.props.borderWidth,
					},
				},
				this.getClose_(),
				this.props.children
			)
		);
	},

	getClose_: function() {
		if (!this.props.onClose) {
			return null;
		}

		return React.DOM.i({
			className: 'fa fa-times fa-lg fa-border cam-dialog-close',
			onClick: this.props.onClose,
		});
	},
});
