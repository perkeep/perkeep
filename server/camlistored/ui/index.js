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

goog.provide('cam.IndexPage');

goog.require('goog.dom');
goog.require('goog.dom.classlist');
goog.require('goog.events.EventHandler');
goog.require('goog.labs.Promise');
goog.require('goog.object');
goog.require('goog.string');
goog.require('goog.Uri');

goog.require('cam.BlobItemContainerReact');
goog.require('cam.BlobItemFoursquareContent');
goog.require('cam.BlobItemGenericContent');
goog.require('cam.BlobItemVideoContent');
goog.require('cam.BlobItemImageContent');
goog.require('cam.BlobItemDemoContent');
goog.require('cam.DetailView');
goog.require('cam.Navigator');
goog.require('cam.NavReact');
goog.require('cam.reactUtil');
goog.require('cam.SearchSession');
goog.require('cam.ServerConnection');

cam.IndexPage = React.createClass({
	displayName: 'IndexPage',

	NAV_WIDTH_CLOSED_: 36,
	NAV_WIDTH_OPEN_: 239,

	THUMBNAIL_SIZES_: [75, 100, 150, 200, 250, 300],

	SEARCH_PREFIX_: {
		RAW: 'raw'
	},

	propTypes: {
		availWidth: React.PropTypes.number.isRequired,
		availHeight: React.PropTypes.number.isRequired,
		config: React.PropTypes.object.isRequired,
		eventTarget: cam.reactUtil.quacksLike({addEventListener:React.PropTypes.func.isRequired}).isRequired,
		history: cam.reactUtil.quacksLike({pushState:React.PropTypes.func.isRequired, replaceState:React.PropTypes.func.isRequired, go:React.PropTypes.func.isRequired, state:React.PropTypes.object}).isRequired,
		location: cam.reactUtil.quacksLike({href:React.PropTypes.string.isRequired, reload:React.PropTypes.func.isRequired}).isRequired,
		serverConnection: React.PropTypes.instanceOf(cam.ServerConnection).isRequired,
		timer: cam.NavReact.originalSpec.propTypes.timer,
	},

	componentWillMount: function() {
		this.baseURL_ = null;
		this.currentSet_ = null;
		this.dragEndTimer_ = 0;
		this.navigator_ = null;
		this.searchSession_ = null;

		// TODO(aa): Move this to index.css once conversion to React is complete (index.css is shared between React and non-React).
		goog.dom.getDocumentScrollElement().style.overflow = 'hidden';

		this.eh_ = new goog.events.EventHandler(this);

		var newURL = new goog.Uri(this.props.location.href);
		this.baseURL_ = newURL.resolve(new goog.Uri(CAMLISTORE_CONFIG.uiRoot));

		this.navigator_ = new cam.Navigator(this.props.eventTarget, this.props.location, this.props.history);
		this.navigator_.onNavigate = this.handleNavigate_;

		this.handleNavigate_(newURL);
	},

	componentDidMount: function() {
		this.eh_.listen(this.props.eventTarget, 'keypress', this.handleKeyPress_);
	},

	componentWillUnmount: function() {
		this.eh_.dispose();
		this.clearDragTimer_();
	},

	getInitialState: function() {
		return {
			currentURL: null,
			dropActive: false,
			isNavOpen: false,
			selection: {},
			thumbnailSizeIndex: 3,
		};
	},

	render: function() {
		return React.DOM.div({onDragEnter:this.handleDragStart_, onDragOver:this.handleDragStart_, onDrop:this.handleDrop_}, [
			this.getNav_(),
			this.getBlobItemContainer_(),
			this.getDetailView_(),
		]);
	},

	handleDragStart_: function(e) {
		this.clearDragTimer_();
		e.preventDefault();
		this.dragEndTimer_ = window.setTimeout(this.handleDragStop_, 2000);
		goog.dom.classlist.add(this.getDOMNode().parentElement, 'cam-dropactive');
	},

	handleDragStop_: function() {
		this.clearDragTimer_();
		goog.dom.classlist.remove(this.getDOMNode().parentElement, 'cam-dropactive');
	},

	clearDragTimer_: function() {
		if (this.dragEndTimer_) {
			window.clearTimeout(this.dragEndTimer_);
			this.dragEndTimer_ = 0;
		}
	},

	handleDrop_: function(e) {
		if (!e.nativeEvent.dataTransfer.files) {
			return;
		}

		e.preventDefault();

		var files = e.nativeEvent.dataTransfer.files;
		var numComplete = 0;
		var sc = this.props.serverConnection;

		console.log('Uploading %d files...', files.length);
		goog.labs.Promise.all(Array.prototype.map.call(files, function(file) {
			var upload = new goog.labs.Promise(sc.uploadFile.bind(sc, file));
			var createPermanode = new goog.labs.Promise(sc.createPermanode.bind(sc));
			return goog.labs.Promise.all([upload, createPermanode]).then(function(results) {
				// TODO(aa): Icky manual destructuring of results. Seems like there must be a better way?
				var fileRef = results[0];
				var permanodeRef = results[1];
				return new goog.labs.Promise(sc.newSetAttributeClaim.bind(sc, permanodeRef, 'camliContent', fileRef));
			}).thenCatch(function(e) {
				console.error('File upload fall down go boom. file: %s, error: %s', file.name, e);
			}).then(function() {
				console.log('%d of %d files complete.', ++numComplete, files.length);
			});
		})).then(function() {
			console.log('All complete');
		});
	},

	handleNavigate_: function(newURL) {
		if (this.state.currentURL) {
			if (this.state.currentURL.getPath() != newURL.getPath()) {
				return false;
			}
		}

		if (!this.isSearchMode_(newURL) && !this.isDetailMode_(newURL)) {
			return false;
		}

		this.updateSearchSession_(newURL);
		this.setState({currentURL: newURL});
		return true;
	},

	updateSearchSession_: function(newURL) {
		var query = newURL.getParameterValue('q');
		if (!query) {
			query = ' ';
		}

		// TODO(aa): Remove this when the server can do something like the 'raw' operator.
		if (goog.string.startsWith(query, this.SEARCH_PREFIX_.RAW + ':')) {
			query = JSON.parse(query.substring(this.SEARCH_PREFIX_.RAW.length + 1));
		}

		if (this.searchSession_ && JSON.stringify(this.searchSession_.getQuery()) == JSON.stringify(query)) {
			return;
		}

		if (this.searchSession_) {
			this.searchSession_.close();
		}

		this.searchSession_ = new cam.SearchSession(this.props.serverConnection, newURL.clone(), query);
	},

	getNav_: function() {
		if (!this.isSearchMode_(this.state.currentURL)) {
			return null;
		}
		return cam.NavReact({key:'nav', ref:'nav', timer:this.props.timer, open:this.state.isNavOpen, onOpen:this.handleNavOpen_, onClose:this.handleNavClose_}, [
			cam.NavReact.SearchItem({key:'search', ref:'search', iconSrc:'magnifying_glass.svg', onSearch:this.setSearch_}, 'Search'),
			this.getCreateSetWithSelectionItem_(),
			cam.NavReact.Item({key:'roots', iconSrc:'icon_27307.svg', onClick:this.handleShowSearchRoots_}, 'Search roots'),
			this.getSelectAsCurrentSetItem_(),
			this.getAddToCurrentSetItem_(),
			this.getClearSelectionItem_(),
			this.getDeleteSelectionItem_(),
			cam.NavReact.Item({key:'up', iconSrc:'up.svg', onClick:this.handleEmbiggen_}, 'Moar bigger'),
			cam.NavReact.Item({key:'down', iconSrc:'down.svg', onClick:this.handleEnsmallen_}, 'Less bigger'),
			cam.NavReact.LinkItem({key:'logo', iconSrc:'/favicon.ico', href:this.baseURL_.toString(), extraClassName:'cam-logo'}, 'Camlistore'),
		]);
	},

	handleNavOpen_: function() {
		this.setState({isNavOpen:true});
	},

	handleNavClose_: function() {
		this.refs.search.clear();
		this.refs.search.blur();
		this.setState({isNavOpen:false});
	},

	handleNewPermanode_: function() {
		this.props.serverConnection.createPermanode(function(p) {
			this.navigator_.navigate(this.getDetailURL_(false, p));
		}.bind(this));
	},

	handleShowSearchRoots_: function() {
		this.setSearch_(this.SEARCH_PREFIX_.RAW + ':' + JSON.stringify({
			permanode: {
				attr: 'camliRoot',
				numValue: {
					min: 1
				}
			}
		}));
	},

	handleSelectAsCurrentSet_: function() {
		this.currentSet_ = goog.object.getAnyKey(this.state.selection);
		this.setState({selection:{}});
	},

	handleAddToSet_: function() {
		this.addMembersToSet_(this.currentSet_, goog.object.getKeys(this.state.selection));
	},

	handleCreateSetWithSelection_: function() {
		var selection = goog.object.getKeys(this.state.selection);
		this.props.serverConnection.createPermanode(function(permanode) {
			this.props.serverConnection.newSetAttributeClaim(permanode, 'title', 'New set', function() {
				this.addMembersToSet_(permanode, selection);
			}.bind(this));
		}.bind(this));
	},

	addMembersToSet_: function(permanode, blobrefs) {
		var numComplete = -1;
		var callback = function() {
			if (++numComplete == blobrefs.length) {
				this.setState({selection:{}});
				this.searchSession_.refreshIfNecessary();
			}
		}.bind(this);

		callback();

		blobrefs.forEach(function(br) {
			this.props.serverConnection.newAddAttributeClaim(permanode, 'camliMember', br, callback);
		}.bind(this));
	},

	handleClearSelection_: function() {
		this.setState({selection:{}});
	},

	handleDeleteSelection_: function() {
		var blobrefs = goog.object.getKeys(this.state.selection);
		var msg = 'Delete';
		if (blobrefs.length > 1) {
			msg += goog.string.subs(' %s items?', blobrefs.length);
		} else {
			msg += ' item?';
		}
		if (!confirm(msg)) {
			return null;
		}

		var numDeleted = 0;
		blobrefs.forEach(function(br) {
			this.props.serverConnection.newDeleteClaim(br, function() {
				if (++numDeleted == blobrefs.length) {
					this.setState({selection:{}});
					this.searchSession_.refreshIfNecessary();
				}
			}.bind(this));
		}.bind(this));
	},

	handleEmbiggen_: function() {
		var newSizeIndex = this.state.thumbnailSizeIndex + 1;
		if (newSizeIndex < this.THUMBNAIL_SIZES_.length) {
			this.setState({thumbnailSizeIndex:newSizeIndex});
		}
	},

	handleEnsmallen_: function() {
		var newSizeIndex = this.state.thumbnailSizeIndex - 1;
		if (newSizeIndex >= 0) {
			this.setState({thumbnailSizeIndex:newSizeIndex});
		}
	},

	handleKeyPress_: function(e) {
		if (String.fromCharCode(e.charCode) == '/') {
			this.refs.nav.open();
			this.refs.search.focus();
			e.preventDefault();
		}
	},

	handleDetailURL_: function(blobref) {
		var m = this.searchSession_.getMeta(blobref);
		var rm = this.searchSession_.getResolvedMeta(blobref);
		return this.getDetailURL_(Boolean(rm && rm.image), m.blobRef);
	},

	getDetailURL_: function(newUI, blobref) {
		var detailURL = this.state.currentURL.clone();
		detailURL.setParameterValue('p', blobref);
		if (newUI) {
			detailURL.setParameterValue('newui', '1');
		} else {
			detailURL.removeParameter('newui');
		}
		return detailURL;
	},

	setSearch_: function(query) {
		var searchURL = this.baseURL_.clone();
		searchURL.setParameterValue('q', query);
		this.navigator_.navigate(searchURL);
	},

	getSelectAsCurrentSetItem_: function() {
		if (goog.object.getCount(this.state.selection) != 1) {
			return null;
		}

		var blobref = goog.object.getAnyKey(this.state.selection);
		if (this.searchSession_.getMeta(blobref).camliType != 'permanode') {
			return null;
		}

		return cam.NavReact.Item({key:'selectascurrent', iconSrc:'target.svg', onClick:this.handleSelectAsCurrentSet_}, 'Select as current set');
	},

	getAddToCurrentSetItem_: function() {
		if (!this.currentSet_ || !goog.object.getAnyKey(this.state.selection)) {
			return null;
		}
		return cam.NavReact.Item({key:'addtoset', iconSrc:'icon_16716.svg', onClick:this.handleAddToSet_}, 'Add to current set');
	},

	getCreateSetWithSelectionItem_: function() {
		var numItems = goog.object.getCount(this.state.selection);
		var label = 'Create set';
		if (numItems == 1) {
			label += ' with item';
		} else if (numItems > 1) {
			label += goog.string.subs(' with %s items', numItems);
		}
		return cam.NavReact.Item({key:'createsetwithselection', iconSrc:'circled_plus.svg', onClick:this.handleCreateSetWithSelection_}, label);
	},

	getClearSelectionItem_: function() {
		if (!goog.object.getAnyKey(this.state.selection)) {
			return null;
		}
		return cam.NavReact.Item({key:'clearselection', iconSrc:'clear.svg', onClick:this.handleClearSelection_}, 'Clear selection');
	},

	getDeleteSelectionItem_: function() {
		if (!goog.object.getAnyKey(this.state.selection)) {
			return null;
		}
		var numItems = goog.object.getCount(this.state.selection);
		var label = 'Delete';
		if (numItems == 1) {
			label += ' selected item';
		} else if (numItems > 1) {
			label += goog.string.subs(' (%s) selected items', numItems);
		}
		// TODO(mpl): better icon in another CL, with Font Awesome.
		return cam.NavReact.Item({key:'deleteselection', iconSrc:'trash.svg', onClick:this.handleDeleteSelection_}, label);
	},

	handleSelectionChange_: function(newSelection) {
		this.setState({selection:newSelection});
	},

	isSearchMode_: function(url) {
		// This is super finicky. We should improve the URL scheme and give things that are different different paths.
		var query = url.getQueryData();
		return query.getCount() == 0 || (query.getCount() == 1 && query.containsKey('q'));
	},

	isDetailMode_: function(url) {
		var query = url.getQueryData();
		return query.containsKey('p') && query.get('newui') == '1';
	},

	getBlobItemContainer_: function() {
		if (!this.isSearchMode_(this.state.currentURL)) {
			return null;
		}

		return cam.BlobItemContainerReact({
			key: 'blobitemcontainer',
			ref: 'blobItemContainer',
			detailURL: this.handleDetailURL_,
			handlers: [
				cam.BlobItemDemoContent.getHandler,
				cam.BlobItemFoursquareContent.getHandler,
				cam.BlobItemImageContent.getHandler,
				cam.BlobItemVideoContent.getHandler,
				cam.BlobItemGenericContent.getHandler // must be last
			],
			history: this.props.history,
			onSelectionChange: this.handleSelectionChange_,
			searchSession: this.searchSession_,
			selection: this.state.selection,
			style: this.getBlobItemContainerStyle_(),
			thumbnailSize: this.THUMBNAIL_SIZES_[this.state.thumbnailSizeIndex],
		});
	},

	getBlobItemContainerStyle_: function() {
		// TODO(aa): Constant values can go into CSS when we switch over to react.
		var style = {
			left: this.NAV_WIDTH_CLOSED_,
			overflowX: 'hidden',
			overflowY: 'scroll',
			position: 'absolute',
			top: 0,
			width: this.getContentWidth_(),
		};

		var closedWidth = style.width;
		var openWidth = closedWidth - this.NAV_WIDTH_OPEN_;
		var openScale = openWidth / closedWidth;

		// TODO(aa): This can move to CSS when the conversion to React is complete.
		style[cam.reactUtil.getVendorProp('transformOrigin')] = 'right top 0';

		// The 3d transform is important. See: https://code.google.com/p/camlistore/issues/detail?id=284.
		var scale = this.state.isNavOpen ? openScale : 1;
		style[cam.reactUtil.getVendorProp('transform')] = goog.string.subs('scale3d(%s, %s, 1)', scale, scale);

		style.height = this.state.isNavOpen ? this.props.availHeight / scale : this.props.availHeight;

		return style;
	},

	getDetailView_: function() {
		if (!this.isDetailMode_(this.state.currentURL)) {
			return null;
		}

		var searchURL = this.baseURL_.clone();
		if (this.state.currentURL.getQueryData().containsKey('q')) {
			searchURL.setParameterValue('q', this.state.currentURL.getParameterValue('q'));
		}

		var oldURL = this.baseURL_.clone();
		oldURL.setParameterValue('p', this.state.currentURL.getParameterValue('p'));

		return cam.DetailView({
			key: 'detailview',
			blobref: this.state.currentURL.getParameterValue('p'),
			history: this.props.history,
			searchSession: this.searchSession_,
			searchURL: searchURL,
			oldURL: oldURL,
			getDetailURL: this.handleDetailURL_,
			navigator: this.navigator_,
			keyEventTarget: this.props.eventTarget,
			width: this.props.availWidth,
			height: this.props.availHeight,
		});
	},

	getContentWidth_: function() {
		return this.props.availWidth - this.NAV_WIDTH_CLOSED_;
	},
});
