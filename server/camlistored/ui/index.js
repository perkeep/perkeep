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

goog.require('goog.array');
goog.require('goog.dom');
goog.require('goog.dom.classlist');
goog.require('goog.events.EventHandler');
goog.require('goog.format');
goog.require('goog.functions');
goog.require('goog.labs.Promise');
goog.require('goog.object');
goog.require('goog.string');
goog.require('goog.Uri');

goog.require('cam.BlobDetail');
goog.require('cam.BlobItemContainerReact');
goog.require('cam.BlobItemDemoContent');
goog.require('cam.BlobItemFoursquareContent');
goog.require('cam.BlobItemGenericContent');
goog.require('cam.BlobItemImageContent');
goog.require('cam.BlobItemTwitterContent');
goog.require('cam.BlobItemVideoContent');
goog.require('cam.blobref');
goog.require('cam.DetailView');
goog.require('cam.Dialog');
goog.require('cam.DirectoryDetail');
goog.require('cam.Header');
goog.require('cam.Navigator');
goog.require('cam.PermanodeDetail');
goog.require('cam.permanodeUtils');
goog.require('cam.reactUtil');
goog.require('cam.SearchSession');
goog.require('cam.ServerConnection');
goog.require('cam.Sidebar');
goog.require('cam.TagsControl');

cam.IndexPage = React.createClass({
	displayName: 'IndexPage',

	SIDEBAR_OPEN_WIDTH_: 250,

	HEADER_HEIGHT_: 38,
	SEARCH_PREFIX_: {
		RAW: 'raw'
	},
	THUMBNAIL_SIZE_: 200,

	SEARCH_SESSION_CACHE_SIZE_: 3,

	// Note that these are ordered by priority.
	BLOB_ITEM_HANDLERS_: [
		cam.BlobItemDemoContent.getHandler,
		cam.BlobItemFoursquareContent.getHandler,
		cam.BlobItemTwitterContent.getHandler,
		cam.BlobItemImageContent.getHandler,
		cam.BlobItemVideoContent.getHandler,
		cam.BlobItemGenericContent.getHandler
	],

	BLOBREF_PATTERN_: new RegExp('^' + cam.blobref.PATTERN + '$'),

	propTypes: {
		availWidth: React.PropTypes.number.isRequired,
		availHeight: React.PropTypes.number.isRequired,
		config: React.PropTypes.object.isRequired,
		eventTarget: React.PropTypes.shape({addEventListener:React.PropTypes.func.isRequired}).isRequired,
		history: React.PropTypes.shape({pushState:React.PropTypes.func.isRequired, replaceState:React.PropTypes.func.isRequired, go:React.PropTypes.func.isRequired, state:React.PropTypes.object}).isRequired,
		openWindow: React.PropTypes.func.isRequired,
		location: React.PropTypes.shape({href:React.PropTypes.string.isRequired, reload:React.PropTypes.func.isRequired}).isRequired,
		scrolling: cam.BlobItemContainerReact.originalSpec.propTypes.scrolling,
		serverConnection: React.PropTypes.instanceOf(cam.ServerConnection).isRequired,
		timer: cam.Header.originalSpec.propTypes.timer,
	},

	componentWillMount: function() {
		this.baseURL_ = null;
		this.dragEndTimer_ = 0;
		this.navigator_ = null;
		this.searchSessionCache_ = [];
		this.targetSearchSession_ = null;
		this.childSearchSession_ = null;

		this.eh_ = new goog.events.EventHandler(this);

		var newURL = new goog.Uri(this.props.location.href);
		this.baseURL_ = newURL.resolve(new goog.Uri(this.props.config.uiRoot));

		this.navigator_ = new cam.Navigator(this.props.eventTarget, this.props.location, this.props.history);
		this.navigator_.onWillNavigate = this.handleWillNavigate_;
		this.navigator_.onDidNavigate = this.handleDidNavigate_;

		this.handleWillNavigate_(newURL);
		this.handleDidNavigate_();
	},

	componentDidMount: function() {
		// TODO(aa): This supports some of the old iframed pages. We can remove it once they are dead.
		goog.global.getSearchSession = function() {
			return this.childSearchSession_;
		}.bind(this);
		this.eh_.listen(this.props.eventTarget, 'keypress', this.handleKeyPress_);
	},

	componentWillUnmount: function() {
		this.eh_.dispose();
		this.clearDragTimer_();
	},

	getInitialState: function() {
		return {
			currentURL: null,
			currentSet: '',
			dropActive: false,
			selection: {},
			serverStatus: null,

			// TODO: This should be calculated by whether selection is empty, and not need separate state.
			sidebarVisible: false,

			uploadDialogVisible: false,
			totalBytesToUpload: 0,
			totalBytesComplete: 0,
		};
	},

	render: function() {
		var aspects = this.getAspects_();
		var selectedAspect = goog.array.findIndex(aspects, function(v) {
			return v.fragment == this.state.currentURL.getFragment();
		}, this);

		if (selectedAspect == -1) {
			selectedAspect = 0;
		}

		var backwardPiggy = false;
		var contentSize = new goog.math.Size(this.props.availWidth, this.props.availHeight - this.HEADER_HEIGHT_);
		return React.DOM.div({onDragEnter:this.handleDragStart_, onDragOver:this.handleDragStart_, onDrop:this.handleDrop_},
			this.getHeader_(aspects, selectedAspect),
			React.DOM.div(
				{
					className: 'cam-content-wrap',
					style: {
						top: this.HEADER_HEIGHT_,
					},
				},
				aspects[selectedAspect] && aspects[selectedAspect].createContent(contentSize, backwardPiggy)
			),
			this.getSidebar_(aspects[selectedAspect]),
			this.getUploadDialog_()
		);
	},

	setSelection_: function(selection) {
		this.props.history.replaceState(cam.object.extend(this.props.history.state, {
			selection: selection,
		}), '', this.props.location.href);

		this.setState({selection: selection});
		this.setState({sidebarVisible: !goog.object.isEmpty(selection)});
	},

	getTargetBlobref_: function(opt_url) {
		var url = opt_url || this.state.currentURL;
		var suffix = url.getPath().substr(this.baseURL_.getPath().length);

		// TODO(aa): Need to implement something like ref.go that knows about the other hash types.
		var match = suffix.match(this.BLOBREF_PATTERN_);
		return match && match[0];
	},

	getAspects_: function() {
		var childFrameClickHandler = this.navigator_.navigate.bind(null, this.navigator_);
		var target = this.getTargetBlobref_();
		var getAspect = function(f) {
			return f(target, this.targetSearchSession_);
		}.bind(this);

		var specificAspects = [
			cam.ImageDetail.getAspect,
			cam.DirectoryDetail.getAspect.bind(null, this.baseURL_, childFrameClickHandler),
		].map(getAspect).filter(goog.functions.identity);

		var generalAspects = [
			this.getSearchAspect_.bind(null, specificAspects),
			cam.PermanodeDetail.getAspect.bind(null, this.props.serverConnection, this.props.timer),
			cam.BlobDetail.getAspect.bind(null, this.getDetailURL_, this.props.serverConnection),
		].map(getAspect).filter(goog.functions.identity);

		return specificAspects.concat(generalAspects);
	},

	getSearchAspect_: function(specificAspects, blobref, targetSearchSession) {
		if (blobref) {
			var m = targetSearchSession.getMeta(blobref);
			if (!m || !m.permanode) {
				// We have a target, but it's not a permanode. So don't show the contents view.
				// TODO(aa): Maybe we do want to for directories though?
				return null;
			}

			// If the permanode already has children, we always show the container view.
			// Otherwise, show the container view only if there is no more specific type.
			var showSearchAspect = false;
			if (cam.permanodeUtils.isContainer(m.permanode)) {
				showSearchAspect = true;
			} else if (!cam.permanodeUtils.getCamliNodeType(m.permanode) && specificAspects.length == 0) {
				showSearchAspect = true;
			}

			if (!showSearchAspect) {
				return null;
			}
		}

		// This can happen when a user types a raw (JSON) query that is invalid.
		if (!this.childSearchSession_) {
			return null;
		}

		return {
			title: blobref ? 'Contents' : 'Search',
			fragment: blobref ? 'contents': 'search',
			createContent: this.getBlobItemContainer_.bind(null, this),
		};
	},

	handleDragStart_: function(e) {
		this.clearDragTimer_();
		e.preventDefault();
		this.dragEndTimer_ = window.setTimeout(this.handleDragStop_, 2000);
		this.setState({
			dropActive: true,
			uploadDialogVisible: false,
		});
	},

	handleDragStop_: function() {
		this.clearDragTimer_();
		this.setState({dropActive: false});
	},

	clearDragTimer_: function() {
		if (this.dragEndTimer_) {
			window.clearTimeout(this.dragEndTimer_);
			this.dragEndTimer_ = 0;
		}
	},

	onUploadStart_: function(files) {
		var numFiles = files.length;
		var totalBytes = Array.prototype.reduce.call(files, function(sum, file) { return sum + file.size; }, 0);

		this.setState({
			dropActive: false,
			totalBytesToUpload: totalBytes,
			totalBytesComplete: 0,
		});

		console.log('Uploading %d files (%d bytes)...', numFiles, totalBytes);
	},

	onUploadProgressUpdate_: function(file) {
		var completedBytes = this.state.totalBytesComplete + file.size;

		this.setState({
			totalBytesComplete: completedBytes
		});

		console.log('Uploaded %d of %d bytes', completedBytes, this.state.totalBytesToUpload);
	},

	onUploadComplete_: function() {
		console.log('Upload complete!');

		this.setState({
			totalBytesToUpload: 0,
			totalBytesComplete: 0,
		});
	},

	handleDrop_: function(e) {
		if (!e.nativeEvent.dataTransfer.files) {
			return;
		}

		e.preventDefault();

		var files = e.nativeEvent.dataTransfer.files;
		var sc = this.props.serverConnection;

		this.onUploadStart_(files);

		goog.labs.Promise.all(
			Array.prototype.map.call(files, function(file) {
				return uploadFileAndCreatePermanode(file)
					.then(transformResults)
					.then(createPermanodeAssociations.bind(this))
					.thenCatch(function(e) {
						console.error('File upload fall down go boom. file: %s, error: %s', file.name, e);
					})
					.then(this.onUploadProgressUpdate_.bind(this, file));
			}.bind(this))
		).thenCatch(function(e) {
			console.error('File upload failed with error: %s', e);
		}).then(this.onUploadComplete_);

		function uploadFileAndCreatePermanode(file) {
			var uploadFile = new goog.labs.Promise(sc.uploadFile.bind(sc, file));
			var createPermanode = new goog.labs.Promise(sc.createPermanode.bind(sc));

			return goog.labs.Promise.all([uploadFile, createPermanode]);
		}

		// 'readable-ify' the blob references returned from upload/create
		function transformResults(blobIds) {
			return {
				'fileRef': blobIds[0],
				'permanodeRef': blobIds[1]
			};
		}

		function createPermanodeAssociations(refs) {
			// associate uploaded file to new permanode
			var camliContent = new goog.labs.Promise(sc.newSetAttributeClaim.bind(sc, refs.permanodeRef, 'camliContent', refs.fileRef));
			var promises = [camliContent];

			// if currently viewing a set, make new permanode a member of the set
			var parentPermanodeRef = this.getTargetBlobref_();
			if (parentPermanodeRef) {
				var camliMember = new goog.labs.Promise(sc.newAddAttributeClaim.bind(sc, parentPermanodeRef, 'camliMember', refs.permanodeRef));
				promises.push(camliMember);
			}

			return goog.labs.Promise.all(promises);
		}
	},

	handleWillNavigate_: function(newURL) {
		if (!goog.string.startsWith(newURL.toString(), this.baseURL_.toString())) {
			return false;
		}

		var targetBlobref = this.getTargetBlobref_(newURL);
		this.updateTargetSearchSession_(targetBlobref);
		this.updateChildSearchSession_(targetBlobref, newURL);
		this.pruneSearchSessionCache_();
		this.setState({currentURL: newURL});
		return true;
	},

	handleDidNavigate_: function() {
		var s = this.props.history.state && this.props.history.state.selection;
		this.setSelection_(s || {});
	},

	updateTargetSearchSession_: function(targetBlobref) {
		if (targetBlobref) {
			this.targetSearchSession_ = this.getSearchSession_(targetBlobref, {blobRefPrefix: targetBlobref});
		} else {
			this.targetSearchSession_ = null;
		}
	},

	updateChildSearchSession_: function(targetBlobref, newURL) {
		var query = newURL.getParameterValue('q');

		if (targetBlobref) {
			query = {
				permanode: {
					relation: {
						relation: 'parent',
						any: { blobRefPrefix: targetBlobref },
					},
				},
			};
		} else if (query) {
			// TODO(aa): Remove this when the server can do something like the 'raw' operator.
			if (goog.string.startsWith(query, this.SEARCH_PREFIX_.RAW + ':')) {
				try {
					query = JSON.parse(query.substring(this.SEARCH_PREFIX_.RAW.length + 1));
				} catch (e) {
					console.error('Raw search is invalid JSON', e);
					query = null;
				}
			}
		} else {
			query = ' ';
		}

		if (query) {
			this.childSearchSession_ = this.getSearchSession_(null, query);
		} else {
			this.childSearchSession_ = null;
		}
	},

	getSearchSession_: function(targetBlobref, query) {
		// This whole business of reusing search session relies on the assumption that we use the same describe rules for both detail queries and search queries.
		var queryString = JSON.stringify(query);
		var cached = goog.array.findIndex(this.searchSessionCache_, function(ss, index) {
			if (targetBlobref && ss.getMeta(targetBlobref)) {
				console.log('Found existing SearchSession for blobref %s at position %d', targetBlobref, index);
				return true;
			} else if (JSON.stringify(ss.getQuery()) == queryString) {
				console.log('Found existing SearchSession for query %s at position %d', queryString, index);
				return true;
			} else {
				return false;
			}
		});

		if (cached > -1) {
			this.searchSessionCache_.splice(0, 0, this.searchSessionCache_.splice(cached, 1)[0]);
			return this.searchSessionCache_[0];
		}

		console.log('Creating new search session for query %s', queryString);
		var ss = new cam.SearchSession(this.props.serverConnection, this.baseURL_.clone(), query);
		this.eh_.listen(ss, cam.SearchSession.SEARCH_SESSION_CHANGED, function() {
			this.forceUpdate();
		});
		this.eh_.listen(ss, cam.SearchSession.SEARCH_SESSION_STATUS, function(e) {
			this.setState({
				serverStatus: e.status,
			});
		});
		this.eh_.listen(ss, cam.SearchSession.SEARCH_SESSION_ERROR, function() {
			this.forceUpdate();
		});
		ss.loadMoreResults();
		this.searchSessionCache_.splice(0, 0, ss);
		return ss;
	},

	pruneSearchSessionCache_: function() {
		for (var i = this.SEARCH_SESSION_CACHE_SIZE_; i < this.searchSessionCache_.length; i++) {
			this.searchSessionCache_[i].close();
		}

		this.searchSessionCache_.length = Math.min(this.searchSessionCache_.length, this.SEARCH_SESSION_CACHE_SIZE_);
	},

	getHeader_: function(aspects, selectedAspectIndex) {
		// We don't show the chooser if there's only one thing to choose from.
		if (aspects.length == 1) {
			aspects = [];
		}

		// TODO(aa): It would be cool to normalize the query and single target case, by supporting searches like is:<blobref>, that way we can always show something in the searchbox, even when we're not in a listview.
		var target = this.getTargetBlobref_();
		var query = '';
		if (target) {
			query = 'ref:' + target;
		} else {
			query = this.state.currentURL.getParameterValue('q') || '';
		}

		return cam.Header(
			{
				currentSearch: query,
				errors: this.getErrors_(),
				height: 38,
				homeURL: this.baseURL_,
				importersURL: this.baseURL_.resolve(new goog.Uri(this.props.config.importerRoot)),
				mainControls: aspects.map(function(val, idx) {
					return React.DOM.a(
						{
							key: val.title,
							className: React.addons.classSet({
								'cam-header-main-control-active': idx == selectedAspectIndex,
							}),
							href: this.state.currentURL.clone().setFragment(val.fragment).toString(),
						},
						val.title
					);
				}, this),
				onUpload: this.handleUpload_,
				onNewPermanode: this.handleCreateSetWithSelection_,
				onSearch: this.setSearch_,
				searchRootsURL: this.getSearchRootsURL_(),
				statusURL: this.baseURL_.resolve(new goog.Uri(this.props.config.statusRoot)),
				ref: 'header',
				timer: this.props.timer,
				width: this.props.availWidth,
			}
		)
	},

	handleNewPermanode_: function() {
		this.props.serverConnection.createPermanode(this.getDetailURL_.bind(this));
	},

	getSearchRootsURL_: function() {
		return this.baseURL_.clone().setParameterValue(
			'q',
			this.SEARCH_PREFIX_.RAW + ':' + JSON.stringify({
				permanode: {
					attr: 'camliRoot',
					numValue: {
						min: 1
					}
				}
			})
		);
	},

	handleSelectAsCurrentSet_: function() {
		this.setState({
			currentSet: goog.object.getAnyKey(this.state.selection),
		});
		this.setSelection_({});
		alert('Now, select the items to add to this set and click "Add to picked set" in the sidebar.\n\n' +
			  'Sorry this is lame, we\'re working on it.');
	},

	handleAddToSet_: function() {
		this.addMembersToSet_(this.state.currentSet, goog.object.getKeys(this.state.selection));
		alert('Done!');
	},

	handleUpload_: function() {
		this.setState({
			uploadDialogVisible: true,
		});
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
				this.setSelection_({});
				this.refreshIfNecessary_();
				this.navigator_.navigate(this.getDetailURL_(permanode));
			}
		}.bind(this);

		callback();

		blobrefs.forEach(function(br) {
			this.props.serverConnection.newAddAttributeClaim(permanode, 'camliMember', br, callback);
		}.bind(this));
	},

	handleClearSelection_: function() {
		this.setSelection_({});
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
					this.setSelection_({});
					this.refreshIfNecessary_();
				}
			}.bind(this));
		}.bind(this));
	},

	handleOpenWindow_: function(url) {
		this.props.openWindow(url);
	},

	handleKeyPress_: function(e) {
		if (e.target.tagName == 'INPUT' || e.target.tagName == 'TEXTAREA') {
			return;
		}

		switch (String.fromCharCode(e.charCode)) {
			case '/': {
				this.refs['header'].focusSearch();
				e.preventDefault();
				break;
			}

			case '|': {
				window.__debugConsoleClient = {
					getSelectedItems: function() {
						return this.state.selection;
					}.bind(this),
					serverConnection: this.props.serverConnection,
				};
				window.open('debug_console.html', 'debugconsole', 'width=400,height=300');
				break;
			}
		}
	},

	handleDetailURL_: function(blobref) {
		return this.getDetailURL_(blobref);
	},

	getDetailURL_: function(blobref) {
		return this.baseURL_.clone().setPath(this.baseURL_.getPath() + blobref);
	},

	setSearch_: function(query) {
		var searchURL;
		var match = query.match(/^ref:(.+)/);
		if (match) {
			searchURL = this.getDetailURL_(match[1]);
		} else {
			searchURL = this.baseURL_.clone().setParameterValue('q', query);
		}
		this.navigator_.navigate(searchURL);
	},

	getSelectAsCurrentSetItem_: function() {
		if (goog.object.getCount(this.state.selection) != 1) {
			return null;
		}

		var blobref = goog.object.getAnyKey(this.state.selection);
		var m = this.childSearchSession_.getMeta(blobref);
		if (!m || m.camliType != 'permanode') {
			return null;
		}

		return React.DOM.button(
			{
				key:'selectascurrent',
				onClick:this.handleSelectAsCurrentSet_
			},
			'Add items to set'
		);
	},

	getAddToCurrentSetItem_: function() {
		if (!this.state.currentSet) {
			return null;
		}

		return React.DOM.button(
			{
				key:'addtoset',
				onClick:this.handleAddToSet_
			},
			'Add to picked set'
		);
	},

	getCreateSetWithSelectionItem_: function() {
		return React.DOM.button(
			{
				key:'createsetwithselection',
				onClick:this.handleCreateSetWithSelection_
			},
			'Create set with items'
		);
	},

	getClearSelectionItem_: function() {
		return React.DOM.button(
			{
				key:'clearselection',
				onClick:this.handleClearSelection_
			},
			'Clear selection'
		);
	},

	getDeleteSelectionItem_: function() {
		return React.DOM.button(
			{
				key:'deleteselection',
				onClick:this.handleDeleteSelection_
			},
			'Delete items'
		);
	},

	getViewOriginalSelectionItem_: function() {
		if (goog.object.getCount(this.state.selection) != 1) {
			return null;
		}

		var blobref = goog.object.getAnyKey(this.state.selection);
		var rm = this.childSearchSession_.getResolvedMeta(blobref);
		if (!rm || !rm.file) {
			return null;
		}

		var fileName = '';
		if (rm.file.fileName) {
			fileName = goog.string.subs('/%s', rm.file.fileName);
		}

		var downloadUrl = goog.string.subs('%s/%s%s', this.props.config.downloadHelper, rm.blobRef, fileName);
		return React.DOM.button(
			{
				key:'viewSelection',
				onClick: this.handleOpenWindow_.bind(null, this, downloadUrl),
			},
			'View original'
		);
	},

	getSidebar_: function(selectedAspect) {
		if (selectedAspect) {
			if (selectedAspect.fragment == 'search' || selectedAspect.fragment == 'contents') {
				return cam.Sidebar( {
					isExpanded: this.state.sidebarVisible,
					mainControls: [
						{
							"displayTitle": "Update Tags",
							"control": this.getTagsControl_()
						}
					].filter(goog.functions.identity),
					selectionControls: [
						this.getClearSelectionItem_(),
						this.getCreateSetWithSelectionItem_(),
						this.getSelectAsCurrentSetItem_(),
						this.getAddToCurrentSetItem_(),
						this.getDeleteSelectionItem_(),
						this.getViewOriginalSelectionItem_(),
					].filter(goog.functions.identity),
					selectedItems: this.state.selection
				});
			}
		}

		return null;
	},

	getTagsControl_: function() {
		return cam.TagsControl(
			{
				selectedItems: this.state.selection,
				searchSession: this.childSearchSession_,
				serverConnection: this.props.serverConnection
			}
		);
	},

	isUploading_: function() {
		return this.state.totalBytesToUpload > 0;
	},

	getUploadDialog_: function() {
		if (!this.state.uploadDialogVisible && !this.state.dropActive && !this.state.totalBytesToUpload) {
			return null;
		}

		var piggyWidth = 88;
		var piggyHeight = 62;
		var borderWidth = 18;
		var w = this.props.availWidth * 0.8;
		var h = this.props.availHeight * 0.8;
		var iconProps = {
			key: 'icon',
			sheetWidth: 10,
			spriteWidth: piggyWidth,
			spriteHeight: piggyHeight,
			style: {
				'margin-right': 3,
				position: 'relative',
				display: 'inline-block',
			}
		};

		function getIcon() {
			if (this.isUploading_()) {
				return cam.SpritedAnimation(cam.object.extend(iconProps, {
					numFrames: 48,
					src: 'glitch/npc_piggy__x1_chew_png_1354829433.png',
				}));
			} else if (this.state.dropActive) {
				return cam.SpritedAnimation(cam.object.extend(iconProps, {
					loopDelay: 4000,
					numFrames: 48,
					src: 'glitch/npc_piggy__x1_look_screen_png_1354829434.png',
					startFrame: 6,
				}));
			} else {
				return cam.SpritedImage(cam.object.extend(iconProps, {
					index: 0,
					src: 'glitch/npc_piggy__x1_look_screen_png_1354829434.png',
				}));
			}
		}

		function getText() {
			if (this.isUploading_()) {
				return goog.string.subs('Uploaded %s (%s%)',
					goog.format.numBytesToString(this.state.totalBytesComplete, 2),
					getUploadProgressPercent.call(this));
			} else {
				return 'Drop files here to upload...';
			}
		}

		function getUploadProgressPercent() {
			if (!this.state.totalBytesToUpload) {
				return 0;
			}

			return Math.round(100 * (this.state.totalBytesComplete / this.state.totalBytesToUpload));
		}

		return cam.Dialog(
			{
				availWidth: this.props.availWidth,
				availHeight: this.props.availHeight,
				width: w,
				height: h,
				borderWidth: borderWidth,
				onClose: this.state.uploadDialogVisible ? this.handleCloseUploadDialog_ : null,
			},
			React.DOM.div(
				{
					className: 'cam-index-upload-dialog',
					style: {
						'text-align': 'center',
						position: 'relative',
						left: -piggyWidth / 2,
						top: (h - piggyHeight - borderWidth * 2) / 2,
					},
				},
				getIcon.call(this),
				getText.call(this)
			)
		);
	},

	handleCloseUploadDialog_: function() {
		this.setState({
			uploadDialogVisible: false,
		});
	},

	handleSelectionChange_: function(newSelection) {
		this.setSelection_(newSelection);
	},

	getBlobItemContainer_: function() {
		var sidebarClosedWidth = this.props.availWidth;
		var sidebarOpenWidth = sidebarClosedWidth - this.SIDEBAR_OPEN_WIDTH_;
		var scale = sidebarOpenWidth / sidebarClosedWidth;

		return cam.BlobItemContainerReact({
			key: 'blobitemcontainer',
			ref: 'blobItemContainer',
			availHeight: this.props.availHeight,
			availWidth: this.props.availWidth,
			detailURL: this.handleDetailURL_,
			handlers: this.BLOB_ITEM_HANDLERS_,
			history: this.props.history,
			onSelectionChange: this.handleSelectionChange_,
			scale: scale,
			scaleEnabled: this.state.sidebarVisible,
			scrolling: this.props.scrolling,
			searchSession: this.childSearchSession_,
			selection: this.state.selection,
			style: this.getBlobItemContainerStyle_(),
			thumbnailSize: this.THUMBNAIL_SIZE_,
		});
	},

	getBlobItemContainerStyle_: function() {
		return {
			left: 0,
			overflowY: this.state.dropActive ? 'hidden' : '',
			position: 'absolute',
			top: 0,
		};
	},

	getContentWidth_: function() {
		return this.props.availWidth;
	},

	refreshIfNecessary_: function() {
		if (this.targetSearchSession_) {
			this.targetSearchSession_.refreshIfNecessary();
		}
		if (this.childSearchSession_) {
			this.childSearchSession_.refreshIfNecessary();
		}
	},

	getErrors_: function() {
		var errors = (this.state.serverStatus && this.state.serverStatus.errors) || [];
		if ((this.targetSearchSession_ && this.targetSearchSession_.hasSocketError()) ||
			(this.childSearchSession_ && this.childSearchSession_.hasSocketError())) {
			errors.push({
				error: 'WebSocket error - click to reload',
				onClick: this.props.location.reload.bind(null, this.props.location, true),
			});
		}
		return errors;
	},
});
