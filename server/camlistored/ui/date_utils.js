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

goog.provide('cam.dateUtils');

cam.dateUtils.formatDateShort = function(date) {
	// TODO(aa): Do something better based on Closure date/i18n utils.
	// I think I would prefer this to return (in en-us) either '11:18 PM', 'Jun 11', or 'June 11 1952', depending on how far back it is. I don't find '5 hours ago' that useful.
	var seconds = Math.floor((Date.now() - date) / 1000);
	var interval = Math.floor(seconds / 31536000);

	return (function() {
		if (interval > 1) {
			return interval + ' years';
		}
		interval = Math.floor(seconds / 2592000);
		if (interval > 1) {
			return interval + ' months';
		}
		interval = Math.floor(seconds / 86400);
		if (interval > 1) {
			return interval + ' days';
		}
		interval = Math.floor(seconds / 3600);
		if (interval > 1) {
			return interval + ' hours';
		}
		interval = Math.floor(seconds / 60);
		if (interval > 1) {
			return interval + ' minutes';
		}
		return Math.floor(seconds) + ' seconds';
	})() + ' ago';
};
