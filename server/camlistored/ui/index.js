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

// IndexPage is the top level React component class that owns all the other
// components of the web UI.
// See the React documentation and in particular
// https://facebook.github.io/react/docs/component-specs.html to learn about
// components.
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

	// Invoked once right before initial rendering. This is essentially IndexPage's
	// constructor. We populate non-React helpers that live for the entire lifetime
	// of IndexPage here.
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

	// Invoked right after initial rendering.
	componentDidMount: function() {
		// TODO(aa): This supports some of the old iframed pages. We can remove it once they are dead.
		goog.global.getSearchSession = function() {
			return this.childSearchSession_;
		}.bind(this);
		this.eh_.listen(this.props.eventTarget, 'keypress', this.handleKeyPress_);
		this.eh_.listen(this.props.eventTarget, 'keyup', this.handleKeyUp_);
	},

	componentWillUnmount: function() {
		this.eh_.dispose();
		this.clearDragTimer_();
	},

	// Invoked once before everything else on initial rendering. Values are
	// subsequently in this.state. We use this to set the initial state and
	// also to document what state fields are possible
	getInitialState: function() {
		return {
			backwardPiggy: false,
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

	// render() is called by React every time a component is determined to need
	// re-rendering. This is typically caused by a call to setState() or a parent
	// component re-rendering.
	render: function() {
		var aspects = this.getAspects_();
		var selectedAspect = goog.array.findIndex(aspects, function(v) {
			return v.fragment == this.state.currentURL.getFragment();
		}, this);

		if (selectedAspect == -1) {
			selectedAspect = 0;
		}

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
				aspects[selectedAspect] && aspects[selectedAspect].createContent(contentSize, this.state.backwardPiggy)
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
		var childFrameClickHandler = this.navigator_.navigate.bind(this.navigator_);
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

		console.log('Completed %d of %d bytes', completedBytes, this.state.totalBytesToUpload);
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
		var parent = this.getTargetBlobref_();

		this.onUploadStart_(files);

		goog.labs.Promise.all(
			Array.prototype.map.call(files, function(file) {
				return uploadFile(file)
					.then(fetchPermanodeIfExists)
					.then(createPermanodeIfNotExists)
					.then(updatePermanodeRef)
					.then(checkExistingCamliMembership)
					.then(createPermanodeAssociations)
					.thenCatch(function(e) {
						console.error('File upload fall down go boom. file: %s, error: %s', file.name, e);
					})
					.then(this.onUploadProgressUpdate_.bind(this, file));
			}.bind(this))
		).thenCatch(function(e) {
			console.error('File upload failed with error: %s', e);
		}).then(this.onUploadComplete_);

		function uploadFile(file) {

			// capture status of upload promise chain
			var status = {
				fileRef: '',
				isCamliMemberOfParent: false,
				parentRef: parent,
				permanodeRef: '',
				permanodeCreated: false
			};

			var uploadFile = new goog.labs.Promise(sc.uploadFile.bind(sc, file));

			return goog.labs.Promise.all([new goog.labs.Promise.resolve(status), uploadFile]);
		}

		function fetchPermanodeIfExists(results) {
			var status = results[0];
			status.fileRef = results[1];

			var getPermanode = new goog.labs.Promise(sc.getPermanodeWithContent.bind(sc, status.fileRef));

			return goog.labs.Promise.all([new goog.labs.Promise.resolve(status), getPermanode]);
		}

		function createPermanodeIfNotExists(results) {
			var status = results[0];
			var permanodeRef = results[1];

			if (!permanodeRef) {
				status.permanodeCreated = true;

				var createPermanode = new goog.labs.Promise(sc.createPermanode.bind(sc));
				return goog.labs.Promise.all([new goog.labs.Promise.resolve(status), createPermanode]);
			}

			return goog.labs.Promise.all([new goog.labs.Promise.resolve(status), new goog.labs.Promise.resolve(permanodeRef)]);
		}

		function updatePermanodeRef(results) {
			var status = results[0];
			status.permanodeRef = results[1];

			return goog.labs.Promise.all([new goog.labs.Promise.resolve(status)]);
		}

		// TODO(mpl): this implementation means that when we're dropping on a set, we send
		// one additional query for each permanode that already exists. So in the worst case,
		// it amounts to one additional query per dropped item (with a small payload/response).
		// Alternatively, we could ask (either by tweaking the search session, or
		// "manually") the server for all the set members and cache the response, which means
		// only one additional query, and we can then do all the tests locally. However, the
		// response size scales with the number of members in the set, so I don't know if it's
		// better. A working example is at
		// https://camlistore-review.googlesource.com/#/c/5345/2 . We should benchmark and/or
		// ask Brad.

		// check, when appropriate, if the permanode is already part of the set we're dropping in.
		function checkExistingCamliMembership(results) {
			var status = results[0];

			// Permanode did not exist before, so it couldn't be a member of any set.
			if (!status.parentRef || status.permanodeCreated) {
				return goog.labs.Promise.all([new goog.labs.Promise.resolve(status), new goog.labs.Promise.resolve(false)]);
			}

			console.log('checking membership');
			var hasMembership = new goog.labs.Promise(sc.isCamliMember.bind(sc, status.permanodeRef, status.parentRef));
			return goog.labs.Promise.all([new goog.labs.Promise.resolve(status), hasMembership]);
		}

		function createPermanodeAssociations(results) {
			var status = results[0];
			status.isCamliMemberOfParent = results[1];

			var promises = [];

			// associate uploaded file to new permanode
			if (status.permanodeCreated) {
				var setCamliContent = new goog.labs.Promise(sc.newSetAttributeClaim.bind(sc, status.permanodeRef, 'camliContent', status.fileRef));
				promises.push(setCamliContent);
			}

			// add CamliMember relationship if viewing a set
			if (status.parentRef && !status.isCamliMemberOfParent) {
				var setCamliMember = new goog.labs.Promise(sc.newAddAttributeClaim.bind(sc, status.parentRef, 'camliMember', status.permanodeRef));
				promises.push(setCamliMember);
			}

			return goog.labs.Promise.all(promises);
		}
	},

	handleWillNavigate_: function(newURL) {
		if (!goog.string.startsWith(newURL.toString(), this.baseURL_.toString())) {
			return false;
		}

		var targetBlobref = this.getTargetBlobref_(newURL);
		this.updateTargetSearchSession_(targetBlobref, newURL);
		this.updateChildSearchSession_(targetBlobref, newURL);
		this.pruneSearchSessionCache_();
		this.setState({
			backwardPiggy: false,
			currentURL: newURL,
		});
		return true;
	},

	handleDidNavigate_: function() {
		var s = this.props.history.state && this.props.history.state.selection;
		this.setSelection_(s || {});
	},

	updateTargetSearchSession_: function(targetBlobref, newURL) {
		this.targetSearchSession_ = null;
		if (targetBlobref) {
			var query = this.queryAsBlob_(targetBlobref);
			var parentPermanode = newURL.getParameterValue('p');
			if (parentPermanode) {
				query = this.queryFromParentPermanode_(parentPermanode);
			} else {
				var queryString = newURL.getParameterValue('q');
				if (queryString) {
					query = this.queryFromSearchParam_(queryString);
				}
			}
			this.targetSearchSession_ = this.getSearchSession_(targetBlobref, query);
		}
	},

	updateChildSearchSession_: function(targetBlobref, newURL) {
		var query = ' ';
		if (targetBlobref) {
			query = this.queryFromParentPermanode_(targetBlobref);
		} else {
			var queryString = newURL.getParameterValue('q');
			if (queryString) {
				query = this.queryFromSearchParam_(queryString);
			}
		}
		this.childSearchSession_ = this.getSearchSession_(null, query);
	},

	queryFromSearchParam_: function(queryString) {
		// TODO(aa): Remove this when the server can do something like the 'raw' operator.
		if (goog.string.startsWith(queryString, this.SEARCH_PREFIX_.RAW + ':')) {
			try {
				return JSON.parse(queryString.substring(this.SEARCH_PREFIX_.RAW.length + 1));
			} catch (e) {
				console.error('Raw search is invalid JSON', e);
				return null;
			}
		} else {
			return queryString;
		}
	},

	queryFromParentPermanode_: function(blobRef) {
		return {
			permanode: {
				relation: {
					relation: 'parent',
					any: { blobRefPrefix: blobRef },
				},
			},
		};
	},

	queryAsBlob_: function(blobRef) {
		return {
			blobRefPrefix: blobRef,
		}
	},

	// Finds an existing cached SearchSession that meets criteria, or creates a new one.
	//
	// If opt_query is present, the returned query must be exactly equivalent.
	// If opt_targetBlobref is present, the returned query must have current results that contain opt_targetBlobref. Otherwise, the returned query must contain the first result.
	//
	// If only opt_targetBlobref is set, then any query that happens to currently contain that blobref is acceptable to the caller.
	getSearchSession_: function(opt_targetBlobref, opt_query) {
		// This whole business of reusing search session relies on the assumption that we use the same describe rules for both detail queries and search queries.
		var queryString = JSON.stringify(opt_query);

		var cached = goog.array.findIndex(this.searchSessionCache_, function(ss) {
			if (opt_targetBlobref) {
				if (!ss.getMeta(opt_targetBlobref)) {
					return false;
				}
				if (!opt_query) {
					return true;
				}
			}

			if (JSON.stringify(ss.getQuery()) != queryString) {
				return false;
			}

			if (!opt_targetBlobref) {
				return !ss.getAround();
			}

			// If there's a targetBlobref, we require that it is not at the very edge of the results so that we can implement lefr/right in detail views.
			var targetIndex = goog.array.findIndex(ss.getCurrentResults().blobs, function(b) {
				return b.blob == opt_targetBlobref;
			});
			return (targetIndex > 0) && (targetIndex < (ss.getCurrentResults().blobs.length - 1));
		});

		if (cached > -1) {
			this.searchSessionCache_.splice(0, 0, this.searchSessionCache_.splice(cached, 1)[0]);
			return this.searchSessionCache_[0];
		}

		console.log('Creating new search session for query %s', queryString);
		var ss = new cam.SearchSession(this.props.serverConnection, this.baseURL_.clone(), opt_query, opt_targetBlobref);
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
				helpURL: this.baseURL_.resolve(new goog.Uri(this.props.config.helpRoot)),
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

	handleKeyUp_: function(e) {
		var isEsc = (e.keyCode == 27);
		var isRight = (e.keyCode == 39);
		var isLeft = (e.keyCode == 37);

		if (isEsc) {
			// TODO: This isn't right, it should go back to the context URL if there is one.
			this.navigator_.navigate(this.baseURL_);
			return;
		}

		if (!isRight && !isLeft) {
			return;
		}

		if (!this.targetSearchSession_) {
			return;
		}

		var blobs = this.targetSearchSession_.getCurrentResults().blobs;
		var target = this.getTargetBlobref_();
		var idx = goog.array.findIndex(blobs, function(item) {
			return item.blob == target;
		});

		if (isRight) {
			if (idx >= (blobs.length - 1)) {
				return;
			}
			idx++;
		} else {
			if (idx <= 0) {
				return;
			}
			idx--;
		}

		var url = this.getDetailURL_(blobs[idx].blob, this.state.currentURL.getFragment());
		['q', 'p'].forEach(function(p) {
			var v = this.state.currentURL.getParameterValue(p);
			if (v) {
				url.setParameterValue(p, v);
			}
		}, this);
		this.navigator_.navigate(url);
		this.setState({
			backwardPiggy: isLeft,
		});
	},

	handleDetailURL_: function(blobref) {
		return this.getChildDetailURL_(blobref);
	},

	getChildDetailURL_: function(blobref, opt_fragment) {
		var query = this.state.currentURL.getParameterValue('q');
		var targetBlobref = this.getTargetBlobref_();
		var url = this.getDetailURL_(blobref, opt_fragment);
		if (targetBlobref) {
			url.setParameterValue('p', targetBlobref);
		} else {
			url.setParameterValue('q', query || ' ');
		}
		return url;
	},

	getDetailURL_: function(blobref, opt_fragment) {
		var query = this.state.currentURL.getParameterValue('q');
		var targetBlobref = this.getTargetBlobref_();
		return url = this.baseURL_.clone().setPath(this.baseURL_.getPath() + blobref).setFragment(opt_fragment || '');
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

		var downloadUrl = goog.string.subs('%s%s%s', this.props.config.downloadHelper, rm.blobRef, fileName);
		return React.DOM.button(
			{
				key:'viewSelection',
				onClick: this.handleOpenWindow_.bind(null, downloadUrl),
			},
			'View original'
		);
	},

	getSidebar_: function(selectedAspect) {
		if (selectedAspect) {
			if (selectedAspect.fragment == 'search' || selectedAspect.fragment == 'contents') {
				var count = goog.object.getCount(this.state.selection);
				return cam.Sidebar( {
					isExpanded: this.state.sidebarVisible,
					header: React.DOM.span(
						{
							className: 'header',
						},
						goog.string.subs('%s selected item%s', count, count > 1 ? 's' : '')
					),
					mainControls: [
						{
							"displayTitle": "Update tags",
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
