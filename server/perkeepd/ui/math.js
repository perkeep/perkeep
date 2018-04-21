goog.provide('cam.math');

goog.require('goog.math.Coordinate');
goog.require('goog.math.Size');

// @param goog.math.Size subject
// @param goog.math.Size frame
// @param =boolean opt_bleed If true, subject will be scaled such that its area is greater or equal to frame. Otherwise, it will be scaled such that its area is less than or equal to frame.
// @return goog.math.Size
cam.math.scaleToFit = function(subject, frame, opt_bleed) {
	var s = (!opt_bleed && subject.aspectRatio() > frame.aspectRatio()) || (opt_bleed && subject.aspectRatio() <= frame.aspectRatio()) ? frame.width / subject.width : frame.height / subject.height;
	return subject.scale(s);
};

// @param goog.math.Size subject
// @param goog.math.Size frame
// @return goog.math.Coordinate the left and top coordinat subject should be positioned at relative to frame to be centered within it. This might be negative if subject is larger than frame.
cam.math.center = function(subject, frame) {
	return new goog.math.Coordinate((frame.width - subject.width) / 2, (frame.height - subject.height) / 2);
};
