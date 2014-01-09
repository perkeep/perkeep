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

goog.provide('cam.Spinner');

goog.require('goog.dom');
goog.require('goog.events.EventHandler');
goog.require('goog.style');
goog.require('goog.math.Coordinate');
goog.require('goog.math.Size');
goog.require('goog.ui.Control');

goog.require('cam.AnimationLoop');
goog.require('cam.style');

// An indeterminite progress meter using the safe icon.
// @param {goog.dom.DomHelper} domHelper
cam.Spinner = function(domHelper) {
	goog.base(this, null, this.dom_);

	this.dom_ = domHelper;
	this.eh_ = new goog.events.EventHandler(this);
	this.animationLoop_ = new cam.AnimationLoop(this.dom_.getWindow());
	this.currentRotation_ = 0;
};

goog.inherits(cam.Spinner, goog.ui.Control);

cam.Spinner.prototype.backgroundImage = "safe-no-wheel.svg";

cam.Spinner.prototype.foregroundImage = "safe-wheel.svg";

cam.Spinner.prototype.degreesPerSecond = 500;

// The origin the safe wheel rotates around, expressed as a fraction of the image's width and height.
cam.Spinner.prototype.wheelRotationOrigin_ = new goog.math.Coordinate(0.37, 0.505);

cam.Spinner.prototype.createDom = function() {
	this.background_ = this.dom_.createDom('div', 'cam-spinner', this.dom_.createDom('div'));
	this.foreground_ = this.background_.firstChild;

	cam.style.setURLStyle(this.background_, 'background-image', this.backgroundImage);
	cam.style.setURLStyle(this.foreground_, 'background-image', this.foregroundImage);

	// TODO(aa): This will need to be configurable. Not sure how makes sense yet.
	var size = new goog.math.Size(75, 75);
	goog.style.setSize(this.background_, size);

	// We should be able to set the origin as a percentage directly, but the browsers end up rounding differently, and we get less off-center spinning on the whole if we set this using pixels.
	var origin = new goog.math.Coordinate(size.width, size.height);
	cam.style.setTransformOrigin(
		this.foreground_,
		origin.scale(this.wheelRotationOrigin_.x, this.wheelRotationOrigin_.y));

	this.eh_.listen(this.animationLoop_, cam.AnimationLoop.FRAME_EVENT_TYPE, this.updateRotation_);

	this.decorateInternal(this.background_);
};

cam.Spinner.prototype.isRunning = function() {
	return this.animationLoop_.isRunning();
};

cam.Spinner.prototype.start = function() {
	this.animationLoop_.start();
};

cam.Spinner.prototype.stop = function() {
	this.animationLoop_.stop();
};

cam.Spinner.prototype.updateRotation_ = function(e) {
	rotation = e.delay / 1000 * this.degreesPerSecond;
	this.currentRotation_ += rotation;
	this.currentRotation_ %= 360;
	cam.style.setRotation(this.foreground_, this.currentRotation_);
};
