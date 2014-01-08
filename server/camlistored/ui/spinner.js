/*
Copyright 2013 The Camlistore Authors

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

goog.provide('camlistore.Spinner');

goog.require('camlistore.AnimationLoop');
goog.require('camlistore.style');
goog.require('goog.dom');
goog.require('goog.events.EventHandler');
goog.require('goog.style');
goog.require('goog.math.Coordinate');
goog.require('goog.math.Size');
goog.require('goog.ui.Control');

// An indeterminite progress meter using the safe icon.
// @param {goog.dom.DomHelper} domHelper
camlistore.Spinner = function(domHelper) {
	goog.base(this, null, this.dom_);

	this.dom_ = domHelper;
	this.eh_ = new goog.events.EventHandler(this);
	this.animationLoop_ = new camlistore.AnimationLoop(this.dom_.getWindow());
	this.currentRotation_ = 0;
};

goog.inherits(camlistore.Spinner, goog.ui.Control);

camlistore.Spinner.prototype.backgroundImage = "safe-no-wheel.svg";

camlistore.Spinner.prototype.foregroundImage = "safe-wheel.svg";

camlistore.Spinner.prototype.degreesPerSecond = 500;

// The origin the safe wheel rotates around, expressed as a fraction of the image's width and height.
camlistore.Spinner.prototype.wheelRotationOrigin_ = new goog.math.Coordinate(0.37, 0.505);

camlistore.Spinner.prototype.createDom = function() {
	this.background_ = this.dom_.createDom('div', 'cam-spinner', this.dom_.createDom('div'));
	this.foreground_ = this.background_.firstChild;

	camlistore.style.setURLStyle(this.background_, 'background-image', this.backgroundImage);
	camlistore.style.setURLStyle(this.foreground_, 'background-image', this.foregroundImage);

	// TODO(aa): This will need to be configurable. Not sure how makes sense yet.
	var size = new goog.math.Size(75, 75);
	goog.style.setSize(this.background_, size);

	// We should be able to set the origin as a percentage directly, but the browsers end up rounding differently, and we get less off-center spinning on the whole if we set this using pixels.
	var origin = new goog.math.Coordinate(size.width, size.height);
	camlistore.style.setTransformOrigin(
		this.foreground_,
		origin.scale(this.wheelRotationOrigin_.x, this.wheelRotationOrigin_.y));

	this.eh_.listen(this.animationLoop_, camlistore.AnimationLoop.FRAME_EVENT_TYPE, this.updateRotation_);

	this.decorateInternal(this.background_);
};

camlistore.Spinner.prototype.isRunning = function() {
	return this.animationLoop_.isRunning();
};

camlistore.Spinner.prototype.start = function() {
	this.animationLoop_.start();
};

camlistore.Spinner.prototype.stop = function() {
	this.animationLoop_.stop();
};

camlistore.Spinner.prototype.updateRotation_ = function(e) {
	rotation = e.delay / 1000 * this.degreesPerSecond;
	this.currentRotation_ += rotation;
	this.currentRotation_ %= 360;
	camlistore.style.setRotation(this.foreground_, this.currentRotation_);
};
