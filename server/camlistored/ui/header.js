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

goog.provide('cam.Header');

goog.require('cam.reactUtil');
goog.require('cam.SpritedImage');

cam.Header = React.createClass({
	displayName: 'Header',

	PIGGY_NATIVE_WIDTH: 88,
	PIGGY_NATIVE_HEIGHT: 62,
	PIGGY_MARGIN: {
		LEFT: 2,
		RIGHT: 5,
		TOP: 0,
		BOTTOM: 2,
	},

	SEARCH_MARGIN: {
		LEFT: 180,
		RIGHT: 145,
	},

	propTypes: {
		currentSearch: React.PropTypes.string,
		height: React.PropTypes.number.isRequired,
		mainControls: React.PropTypes.arrayOf(React.PropTypes.renderable),
		onHome: React.PropTypes.func,
		onNewPermanode: React.PropTypes.func,
		onSearch: React.PropTypes.func,
		onSearchRoots: React.PropTypes.func,
		subControls: React.PropTypes.arrayOf(React.PropTypes.renderable),
		timer: React.PropTypes.shape({setTimeout:React.PropTypes.func.isRequired, clearTimeout:React.PropTypes.func.isRequired}).isRequired,
		width: React.PropTypes.number.isRequired,
	},

	focusSearch: function() {
		this.getSearchNode_().focus();
		this.getSearchNode_().select();
	},

	getInitialState: function() {
		return {
			currentSearch: this.props.currentSearch,
			menuVisible: false,
		};
	},

	componentWillReceiveProps: function(nextProps) {
		if (nextProps.currentSearch != this.props.currentSearch) {
			this.setState({currentSearch: nextProps.currentSearch});
		}
	},

	render: function() {
		return React.DOM.div(
			{
				className: React.addons.classSet({
					'cam-header': true,
					'cam-header-sub-active': this.props.subControls.length,
				}),
				style: {
					width: this.props.width,
				},
			},
			React.DOM.table(
				{
					className: 'cam-header-main',
				},
				React.DOM.tr(null,
					this.getPiggy_(),
					this.getTitle_(),
					this.getSearchbox_(),
					this.getMainControls_()
				)
			),
			this.getSubheader_(),
			this.getMenuDropdown_()
		)
	},

	getPiggy_: function() {
		return React.DOM.td(
			{
				className: 'cam-header-item',
				style: {
					minWidth: this.getPiggyWidth_() + this.PIGGY_MARGIN.LEFT + this.PIGGY_MARGIN.RIGHT,
				},
				onMouseEnter: this.handleMouseEnter_,
				onMouseLeave: this.handleMouseLeave_,
			},
			cam.SpritedImage({
				index: 5,
				src: 'glitch/npc_piggy__x1_chew_png_1354829433.png',
				sheetWidth: 11,
				spriteWidth: this.PIGGY_NATIVE_WIDTH,
				spriteHeight: this.PIGGY_NATIVE_HEIGHT,
				style: cam.reactUtil.getVendorProps({
					position: 'absolute',
					left: this.PIGGY_MARGIN.LEFT,
					top: this.PIGGY_MARGIN.TOP,
					transform: 'scale(' + this.getPiggyScale_() + ')',
					transformOrigin: '0 0',
				})
			})
		)
	},

	getTitle_: function() {
		return React.DOM.td(
			{
				className: 'cam-header-item cam-header-title',
				onMouseEnter: this.handleMouseEnter_,
				onMouseLeave: this.handleMouseLeave_,
			},
			React.DOM.span(null, 'Pudgy'),
			React.DOM.span(null, '\u25BE')
		);
	},

	getSearchbox_: function() {
		return React.DOM.td(
			{
				className: 'cam-header-item',
				style: {
					width: '100%',
				}
			},
			React.DOM.form(
				{
					onSubmit: this.handleSearchSubmit_,
				},
				React.DOM.input(
					{
						onChange: this.handleSearchChange_,
						placeholder: 'Search...',
						ref: 'searchbox',
						value: this.state.currentSearch,
					}
				)
			)
		)
	},

	getMainControls_: function() {
		return React.DOM.td(
			{
				className: React.addons.classSet({
					'cam-header-item': true,
					'cam-header-main-controls': true,
					'cam-header-main-controls-empty': !this.props.mainControls.length,
				}),
			},
			this.props.mainControls
		);
	},

	getMenuDropdown_: function() {
		return React.DOM.div(
			{
				className: 'cam-header-menu-dropdown',
				onClick: this.handleDropdownClick_,
				onMouseEnter: this.handleMouseEnter_,
				onMouseLeave: this.handleMouseLeave_,
				style: cam.reactUtil.getVendorProps({
					transform: 'translate3d(0, ' + this.getMenuTranslate_() + '%, 0)',
				}),
			},
			this.getMenuItem_('Home', this.props.onHome),
			this.getMenuItem_('New permanode', this.props.onNewPermanode),
			this.getMenuItem_('Search roots', this.props.onSearchRoots)
		);
	},

	getMenuItem_: function(text, handler) {
		return React.DOM.div(
			{
				className: 'cam-header-menu-item',
				onClick: handler,
			},
			text
		);
	},

	getSubheader_: function() {
		return React.DOM.div(
			{
				className: 'cam-header-sub',
			},
			this.props.subControls
		);
	},

	getMenuTranslate_: function() {
		if (this.state.menuVisible) {
			return 0;
		} else {
			// 110% because it has a shadow that we don't want to double-up with the shadow from the header.
			return -110;
		}
	},

	getPiggyHeight_: function() {
		return this.props.height - this.PIGGY_MARGIN.TOP - this.PIGGY_MARGIN.BOTTOM;
	},

	getPiggyWidth_: function() {
		return this.getPiggyScale_() * this.PIGGY_NATIVE_WIDTH;
	},

	getPiggyScale_: function() {
		return this.getPiggyHeight_() / this.PIGGY_NATIVE_HEIGHT;
	},

	handleMouseEnter_: function() {
		this.clearTimer_();
		this.setTimer_(true);
	},

	handleMouseLeave_: function() {
		this.clearTimer_();
		this.setTimer_(false);
	},

	handleDropdownClick_: function() {
		this.clearTimer_();
		this.setState({menuVisible:false});
	},

	setTimer_: function(show) {
		this.timerId_ = this.props.timer.setTimeout(this.handleTimer_.bind(null, show), 250);
	},

	clearTimer_: function() {
		if (this.timerId_) {
			this.props.timer.clearTimeout(this.timerId_);
		}
	},

	handleTimer_: function(show) {
		this.setState({menuVisible:show});
	},

	handleSearchChange_: function(e) {
		this.setState({currentSearch: e.target.value});
	},

	handleSearchSubmit_: function(e) {
		this.props.onSearch(this.getSearchNode_().value);
		e.preventDefault();
	},

	getSearchNode_: function() {
		return this.refs['searchbox'].getDOMNode();
	},
});
