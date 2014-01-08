/*
Copyright 2014 Google Inc.

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

goog.provide('cam.WorkerMessageRouter');

goog.require('goog.string');

// Convenience for sending request/response style messages to and from workers.
// @param {!Worker} worker The DOM worker to wrap.
// @constructor
cam.WorkerMessageRouter = function(worker) {
	this.worker_ = worker;
	this.nextMessageId_ = 1;

	// name->handler - See registerHandler()
	// @type Object.<string, function(*, function(*))>
	this.handlers_ = {};

	// messageid->callback - See sendMessage()
	// @type Object.<number, function(*)>
	this.pendingMessages_ = {};

	this.worker_.addEventListener('message', this.handleMessage_.bind(this));
};

// Send a message over the worker, optionally expecting a response.
// @param {!string} name The name of the message to send.
// @param {!*} msg The message content
// @param {?function(*)} opt_callback The function to receive the response.
cam.WorkerMessageRouter.prototype.sendMessage = function(name, msg, opt_callback) {
	var messageId = 0;
	if (opt_callback) {
		messageId = this.nextMessageId_++;
		this.pendingMessages_[messageId] = opt_callback;
	}
	this.worker_.postMessage({
		messageId: messageId,
		name: name,
		message: msg
	});
};

// Registers a function to handle a particular named message type.
// @param {!string} name The name of the message type to handle.
// @param {!function(*, function(*))} handler The function to call to return the reply to the client.
cam.WorkerMessageRouter.prototype.registerHandler = function(name, handler) {
	this.handlers_[name] = handler;
};

cam.WorkerMessageRouter.prototype.handleMessage_ = function(e) {
	if (!goog.isObject(e.data) || !goog.isDef(e.data.messageId)) {
		return;
	}

	if (goog.isDef(e.data.name)) {
		this.handleRequest_(e.data);
	} else {
		this.handleReply_(e.data);
	}
};

cam.WorkerMessageRouter.prototype.handleRequest_ = function(request) {
	var handler = this.handlers_[request.name];
	if (!handler) {
		throw new Error(goog.string.subs('No registered handler with name: %s', request.name));
	}

	var sendReply = function(reply) {
		if (!request.messageId) {
			return;
		}
		this.worker_.postMessage({
			messageId: request.messageId,
			message: reply
		});
	}.bind(this);

	handler(request.message, sendReply);
};

cam.WorkerMessageRouter.prototype.handleReply_ = function(reply) {
	var callback = this.pendingMessages_[reply.messageId];
	if (!callback) {
		throw new Error('Could not find callback for pending message: %s', reply.messageId);
	}
	delete this.pendingMessages_[reply.messageId];
	callback(reply.message);
};
