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

var assert = require('assert');

goog.require('cam.SearchSession');


function MockServerConnection(response) {
	this.response_ = response;
}

MockServerConnection.prototype.search = function(query, describe, limit, continuationToken, callback) {
	setImmediate(callback.bind(null, this.response_));
};


describe('cam.SearchSession', function() {
	var session = null;
	var response = {
		blobs: [
			{'blob': 'a'},
			{'blob': 'b'},
			{'blob': 'c'},
			{'blob': 'd'},
			{'blob': 'e'},
		],
		description: {
			meta: {
				a: {
					blobRef: 'a',
					camliType: 'file',
					file: {
						fileName: 'foo.txt',
					},
				},
				a2: {
					blobRef: 'a2',
					camliType: 'file',
					file: {
					},
				},
				b: {
					blobRef: 'b',
					camliType: 'permanode',
					permanode: {
						attr: {
							camliContent: ['a'],
							title: ['permanode b'],
						}
					}
				},
				b2: {
					blobRef: 'b2',
					camliType: 'permanode',
					permanode: {
						attr: {
							camliContent: ['a'],
						}
					}
				},
				c: {
					blobRef: 'c',
					camliType: 'permanode',
					permanode: {
						attr: {
						},
					}
				},
				d: {
					blobRef: 'd',
					camliType: 'permanode',
					permanode: {
						attr: {
							camliContent: ['b'],
						}
					}
				},
				e: {
					blobRef: 'e',
					camliType: 'permanode',
					permanode: {
						attr: {
							camliContent: ['_non_existant_'],
							title: 'permanode e',
						}
					}
				},
			}
		}
	};

	before(function(done) {
		var currentUri = null;
		var query = null;
		session = new cam.SearchSession(new MockServerConnection(response), currentUri, query);
		session.addEventListener(cam.SearchSession.SEARCH_SESSION_CHANGED, function() {
			assert.equal(response.description.meta.a, session.getResolvedMeta('a'));
			done();
		});
		session.loadMoreResults();
	});

	describe('#getResolvedMeta', function() {
		it('should resolve blobrefs correctly', function() {
			// a is not a permanode, so its resolved value is itself.
			assert.equal(response.description.meta.a, session.getResolvedMeta('a'));

			// b is a permanode that points to a.
			assert.equal(response.description.meta.a, session.getResolvedMeta('b'));

			// c is a permanode, but has no camliContent, so its resolved value is itself.
			assert.equal(response.description.meta.c, session.getResolvedMeta('c'));

			// We currently only resolve one level of indirection via permanodes.
			assert.equal(response.description.meta.b, session.getResolvedMeta('d'));

			// e is a permanode, but its camliContent doesn't exist. This is legitimate and can happen for a variety of reasons (e.g., during sync).
			assert.equal(null, session.getResolvedMeta('e'));

			// z doesn't exist at all.
			assert.equal(null, session.getResolvedMeta('z'));
		});
	});

	describe('#getTitle', function() {
		it('should create correct titles', function() {
			assert.strictEqual(response.description.meta.a.file.fileName, session.getTitle('a'));
			assert.strictEqual('', session.getTitle('a2'));
			assert.strictEqual('permanode b', session.getTitle('b'));
			assert.strictEqual(response.description.meta.a.file.fileName, session.getTitle('b2'));
			assert.strictEqual('permanode e', session.getTitle('e'));
		});
	});
});

describe('cam.SearchSession', function() {
	var session = null;
	var response = {};

	before(function() {
		var currentUri = null;
		var query = null;
		session = new cam.SearchSession(new MockServerConnection(response), currentUri, query);
	});

	describe('new session, no results', function() {
		it('should not hit a null', function() {
			// resetData_ gives us a safe to use data_ (non null fields).
			assert(session.data_.blobs);
			assert(session.data_.description);
			assert(session.data_.description.meta);
		});
	});

});
