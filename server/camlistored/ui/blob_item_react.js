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

goog.provide('cam.BlobItemReact');

goog.require('goog.string');
goog.require('goog.math.Coordinate');

cam.BlobItemReact = React.createClass({
	displayName: 'BlobItemReact',

	propTypes: {
		blobref: React.PropTypes.string.isRequired,
		checked: React.PropTypes.bool.isRequired,
		onCheckClick: React.PropTypes.func,  // (string,event)->void
		onWheel: React.PropTypes.func.isRequired,
		position: React.PropTypes.instanceOf(goog.math.Coordinate).isRequired,
	},

	getInitialState: function() {
		return {
			hovered: false,
		};
	},

	render: function() {
		return React.DOM.div({
				className: this.getRootClassName_(),
				style: this.getRootStyle_(),
				onMouseEnter: this.handleMouseEnter_,
				onMouseLeave: this.handleMouseLeave_,
				onWheel: this.handleWheel_,
			},
			this.getCheckmark_(),
			this.props.children
		);
	},

	getRootClassName_: function() {
		return React.addons.classSet({
			'cam-blobitem': true,
			'goog-control-hover': this.state.hovered,
			'goog-control-checked': this.props.checked,
		});
	},

	getCheckmark_: function() {
		if (this.props.onCheckClick) {
			return React.DOM.div({className:'checkmark', onClick:this.handleCheckClick_});
		} else {
			return null;
		}
	},

	getRootStyle_: function() {
		return {
			left: this.props.position.x,
			top: this.props.position.y,
		};
	},

	handleMouseEnter_: function() {
		this.setState({hovered:true});
	},

	handleMouseLeave_: function() {
		this.setState({hovered:false});
	},

	handleCheckClick_: function(e) {
		this.props.onCheckClick(this.props.blobref, e);
	},

	handleWheel_: function() {
		if (this.props.onWheel) {
			this.props.onWheel(this);
		}
	},
});
