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

goog.provide('cam.PyramidThrobber');

goog.require('goog.math.Coordinate');
goog.require('goog.math.Size');

cam.PyramidThrobber = React.createClass({
	propTypes: {
		pos: React.PropTypes.instanceOf(goog.math.Coordinate),
	},

	render: function() {
		return React.DOM.div({style:this.getStyle_(), className:'cam-pyramid-throbber'},
			React.DOM.div({className:'lefttop'}),
			React.DOM.div({className:'leftbottom'}),
			React.DOM.div({className:'righttop'}),
			React.DOM.div({className:'rightbottom'})
		);
	},

	getStyle_: function() {
		var result = {};
		if (goog.isDef(this.props.pos)) {
			result.left = this.props.pos.x;
			result.top = this.props.pos.y;
		}
		return result;
	}
});

cam.PyramidThrobber.SIZE = new goog.math.Size(70, 85);
