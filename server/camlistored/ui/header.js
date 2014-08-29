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

goog.require('goog.Uri');

goog.require('cam.reactUtil');
goog.require('cam.SpritedImage');

cam.Header = React.createClass({
	displayName: 'Header',

	PIGGY_NATIVE_WIDTH: 88,
	PIGGY_NATIVE_HEIGHT: 62,
	PIGGY_MARGIN: {
		LEFT: 1,
		RIGHT: 4,
		TOP: -1,
		BOTTOM: 1,
	},

	SEARCH_MARGIN: {
		LEFT: 180,
		RIGHT: 145,
	},

	propTypes: {
		homeURL: React.PropTypes.instanceOf(goog.Uri).isRequired,
		currentSearch: React.PropTypes.string,
		height: React.PropTypes.number.isRequired,
		mainControls: React.PropTypes.arrayOf(React.PropTypes.renderable),
		onNewPermanode: React.PropTypes.func,
		onSearch: React.PropTypes.func,
		searchRootsURL: React.PropTypes.instanceOf(goog.Uri).isRequired,
		statusURL: React.PropTypes.instanceOf(goog.Uri).isRequired,
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
			serverStatus: null,
			menuVisible: false,
		};
	},

	componentWillMount: function() {
		var req = new XMLHttpRequest();
		req.open('GET', this.props.statusURL.resolve(new goog.Uri('status.json')), true);
		req.onload = this.handleStatusLoad_;
		req.send(null);
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
		var props = {
			sheetWidth: 11,
			spriteWidth: this.PIGGY_NATIVE_WIDTH,
			spriteHeight: this.PIGGY_NATIVE_HEIGHT,
			style: cam.reactUtil.getVendorProps({
				position: 'absolute',
				left: this.PIGGY_MARGIN.LEFT,
				top: this.PIGGY_MARGIN.TOP,
				transform: 'scale(' + this.getPiggyScale_() + ')',
				transformOrigin: '0 0',
			}),
		};

		var image = function(errors) {
			if (errors.length) {
				return cam.SpritedAnimation(cam.object.extend(props, {
					key: 'error',
					loopDelay: 10 * 1000,
					numFrames: 65,
					src: 'glitch/npc_piggy__x1_too_much_nibble_png_1354829441.png',
				}));
			} else {
				return cam.SpritedImage(cam.object.extend(props, {
					key: 'ok',
					index: 5,
					src: 'glitch/npc_piggy__x1_chew_png_1354829433.png',
				}));
			}
		};

		return React.DOM.td(
			{
				className: 'cam-header-item',
				style: {
					minWidth: this.getPiggyWidth_() + this.PIGGY_MARGIN.LEFT + this.PIGGY_MARGIN.RIGHT,
				},
				onClick: this.handleClick_,
				onMouseEnter: this.handleMouseEnter_,
				onMouseLeave: this.handleMouseLeave_,
			},
			image(this.getErrors_())
		)
	},

	getTitle_: function() {
		return React.DOM.td(
			{
				className: 'cam-header-item cam-header-title',
				onClick: this.handleClick_,
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
		var errorItems = this.getErrors_().map(function(err) {
			var children = [
				React.DOM.i({className: 'fa fa-exclamation-circle cam-header-menu-item-icon'}),
				err.error
			];
			return this.getMenuItemLink_(children, err.url, 'cam-header-menu-item-error');
		}, this);

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
			this.getMenuItemLink_('Home', this.props.homeURL),

			// TODO(aa): Create a new permanode UI that delays creating the permanode until the user confirms, then change this to a link to that UI.
			// TODO(aa): Also I keep going back and forth about whether we should call this 'permanode' or 'set' in the UI. Hrm.
			this.getMenuItemButton_('New set', this.props.onNewPermanode),

			this.getMenuItemLink_('Importers', this.state.serverStatus && new goog.Uri(this.state.serverStatus.importerRoot)),
			this.getMenuItemLink_('Server status', this.props.statusURL),
			this.getMenuItemLink_('Search roots', this.props.searchRootsURL),
			errorItems
		);
	},

	getMenuItemLink_: function(text, link, opt_class) {
		if (!text || !link) {
			return null;
		}

		var className = 'cam-header-menu-item';
		if (opt_class) {
			className += ' ' + opt_class;
		}
		return React.DOM.a(
			{
				className: className,
				href: link.toString(),
			},
			text
		);
	},

	// TODO(aa): All the menu items should ideally be links, so that you can right-click them and so-on.
	getMenuItemButton_: function(text, handler) {
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

	handleClick_: function() {
		this.setState({menuVisible: !this.state.menuVisible});
	},

	handleMouseEnter_: function() {
		this.clearTimer_();
		this.setTimer_(true);
	},

	handleMouseLeave_: function() {
		this.clearTimer_();
		this.setTimer_(false);
	},

	handleDropdownClick_: function(e) {
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

	handleStatusLoad_: function(e) {
		this.setState({
			serverStatus: JSON.parse(e.target.responseText),
		});
	},

	getErrors_: function() {
		return (this.state.serverStatus && this.state.serverStatus.errors) || [];
	},
});
