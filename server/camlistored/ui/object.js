/**
 * Object related utilities beyond what exist in Closure.
 */
goog.provide('object');

function extend(o, n) {
	var obj = {};
	goog.mixin(obj, o);
	goog.mixin(obj, n);
	return obj;
}
