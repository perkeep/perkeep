/*
Copyright 2014 The Camlistore Authors.

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

goog.provide('cam.reactUtil');

goog.require('goog.string');

cam.reactUtil.mapOf = function(validator) {
	var validator = function(props, propName, componentName) {
		if (!props[propName]) {
			return;
		}

		React.PropTypes.isObject(props, propName, componentName);

		for (var child in props[propName]) {
			var childName = goog.string.subs('%s[%s]', componentName, child);
			validator(props[propName], child, childName);
		}
	};

	validator.isRequired = React.PropTypes.object.isRequired;
	return validator;
};

// Returns the appropriate vendor prefixed style property name. This is figured out by testing the presence of various property names on an actual DOM style object.
// The returned property is of the form 'fooBar' (if no prefix is needed), or 'WebkitFooBar' if a prefix is needed, which is the form React expects.
// @param {string} prop The property name to find.
// @param {CSSStyleDeclaration=} style A style object to test on. This can be any DOM style object, e.g., document.body.style.
// @return {?string} The appropriate property name to use, or null if the property is not supported in this environment.
cam.reactUtil.getVendorProp = function(prop, opt_testStyle) {
	if (!goog.isDef(opt_testStyle)) {
		opt_testStyle = document.body.style;
	}

	if (goog.isDef(opt_testStyle[prop])) {
		return prop;
	}

	var prefixes = ['webkit', 'moz', 'ie'];
	for (var i = 0, p; p = prefixes[i]; i++) {
		var candidate = p + goog.string.toTitleCase(prop);
		if (goog.isDef(opt_testStyle[candidate])) {
			// React expects vendor prefixed property names to be TitleCase.
			return goog.string.toTitleCase(candidate);
		}
	}

	return null;
};

// Returns a copy of an object with all properties vendor-prefixed as required by the current ua.
// @param {object} o The object to fix.
// @param {CSSStyleDeclaration=} style A style object to test on. This can be any DOM style object, e.g., document.body.style.
// @return {object} A copy of o with all properties vendor-prefixed as appropriate.
cam.reactUtil.getVendorProps = function(o, opt_testStyle) {
	var n = {};
	for (var p in o) {
		n[cam.reactUtil.getVendorProp(p, opt_testStyle)] = o[p];
	}
	return n;
};

// Like cam.object.extend(), except that special care is taken to also merge together some known child properties that are part of React specifications.
// @param Object parentSpec
// @param Object childSpec
// @return Object merged spec
cam.reactUtil.extend = function(parentSpec, childSpec) {
	var result = cam.object.extend(parentSpec, childSpec);
	if (childSpec.propTypes) {
		result.propTypes = cam.object.extend(parentSpec.propTypes, childSpec.propTypes);
	}
	return result;
}
