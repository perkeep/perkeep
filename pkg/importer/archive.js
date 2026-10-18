/*
Copyright 2026 The Perkeep Authors

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

// The ZIP goes directly from the browser's File to the discovered upload
// helper. Only the resulting file-schema reference goes to the archive API.
(function() {
	"use strict";

	const maxFileSize = 32 * 1024 * 1024 * 1024;
	const omitAuthToken = "OmitAuthToken";
	const validRef = value => typeof value === "string" && /^(sha1-[a-f0-9]{40}|sha224-[a-f0-9]{56}|sha256-[a-f0-9]{64})$/.test(value);
	const object = value => value !== null && typeof value === "object" && !Array.isArray(value);
	const boundedText = value => value.slice(0, 2048);

	function requireField(ok, description) {
		if (!ok) {
			throw new Error("Invalid server response: " + description + ".");
		}
	}

	function parseReply(text, status) {
		let result;
		try {
			result = JSON.parse(text);
		} catch (_) {
			// Preserve the HTTP failure even when an authentication proxy serves HTML.
		}
		if (status < 200 || status >= 300) {
			const detail = object(result) && typeof result.error === "string" ? ": " + boundedText(result.error) : "";
			const auth = status === 401 || status === 403;
			const error = new Error((auth ? "Authentication failed" : "HTTP request failed") + " (" + status + ")" + detail);
			error.authFailure = auth;
			throw error;
		}
		requireField(object(result), "expected a JSON object");
		return result;
	}

	function validateIdentity(identity) {
		requireField(object(identity), "missing archive identity");
		for (const key of ["userName", "email", "name"]) {
			requireField(identity[key] === undefined || typeof identity[key] === "string", "invalid identity " + key);
		}
		requireField(Boolean((identity.userName || "").trim() || (identity.email || "").trim()), "missing username or email");
	}

	function validateStatus(status, accountRef, fileRef) {
		requireField(object(status), "missing archive status");
		requireField(validRef(status.accountRef) && status.accountRef === accountRef, "status account reference does not match");
		requireField(validRef(status.fileRef) && (!fileRef || status.fileRef === fileRef), "status file reference does not match");
		requireField(["running", "complete", "error", "interrupted"].includes(status.state), "unknown archive state");
		requireField(status.error === undefined || typeof status.error === "string", "invalid status error");
		const progress = status.progress;
		requireField(object(progress), "missing import progress");
		for (const key of ["itemsTotal", "itemsDone", "itemsUnchanged", "warningCount"]) {
			requireField(Number.isSafeInteger(progress[key]) && progress[key] >= 0, "invalid progress " + key);
		}
		requireField(progress.warnings === undefined || progress.warnings === null ||
			(Array.isArray(progress.warnings) && progress.warnings.every(warning => typeof warning === "string")), "invalid import warnings");
	}

	class ArchiveImport {
		// Optional browser primitives let the standalone browser tests exercise the
		// real controller without an importer server or exposing credentials.
		constructor(root, options) {
			this.root = root;
			this.env = Object.assign({
				fetch: window.fetch.bind(window),
				createXHR: () => new XMLHttpRequest(),
				navigate: url => window.location.assign(url),
				reload: () => window.location.reload(),
				setTimeout: (fn, delay) => window.setTimeout(fn, delay),
				clearTimeout: id => window.clearTimeout(id),
				location: window.location.href,
			}, options);
			this.nodes = {};
			for (const name of ["upload-form", "file", "upload", "cancel", "progress", "message", "error", "file-info", "file-ref", "confirm", "identity", "account-match", "start", "account-link-container", "account-link", "warnings", "retry"]) {
				this.nodes[name] = root.querySelector("#archive-" + name);
			}
			this.nodes.pause = root.querySelector("#archive-pause") || document.getElementById("archive-pause");
			this.fileRef = root.dataset.fileRef || "";
			this.accountRef = root.dataset.accountRef || "";
			this.accountPage = Boolean(this.accountRef);
			// Only the first successful account-page GET may replace the rendered
			// input with the latest durable input. Later polls must remain pinned.
			this.initialStatus = this.accountPage;
			this.phase = "idle";
			this.serial = 0;
			this.busy = false;
			this.disposed = false;
			this.inspected = false;
			this.confirmed = false;
			this.retry = null;
			this.timer = null;
			this.abortController = null;
			this.xhr = null;

			this.nodes["upload-form"].addEventListener("submit", event => {
				event.preventDefault();
				this.upload();
			});
			this.nodes.file.addEventListener("change", () => {
				if (this.busy || this.accountPage || this.phase === "accepted") {
					return;
				}
				this.setFileRef("");
				this.inspected = false;
				this.confirmed = false;
				this.inspectedAccount = "";
				this.retry = null;
				this.phase = "idle";
				this.clearError();
				this.nodes.message.textContent = "";
				this.nodes["account-link-container"].hidden = true;
				this.nodes.progress.hidden = true;
				this.nodes.warnings.hidden = true;
				this.render();
			});
			this.nodes.start.addEventListener("click", () => {
				if (this.inspected && this.phase === "confirm" && !this.busy) {
					this.confirmed = true;
					this.start();
				}
			});
			this.nodes.cancel.addEventListener("click", () => this.cancel());
			this.nodes.retry.addEventListener("click", () => {
				if (this.retry && !this.busy && !this.disposed) {
					this.retry.action();
				}
			});
			this.onPageShow = event => {
				if (event.persisted) {
					this.env.reload();
				}
			};
			this.onPageHide = () => {
				this.dispose();
				// A BFCache restoration must rediscover durable state and create fresh
				// listeners, rather than reviving a disposed/accepted controller.
				window.addEventListener("pageshow", this.onPageShow, {once: true});
			};
			window.addEventListener("pagehide", this.onPageHide);
			this.ready = this.initialize();
		}

		sameOriginURL(value) {
			if (typeof value !== "string" || !value.trim()) {
				throw new Error("Missing archive URL configuration.");
			}
			const url = new URL(value, this.env.location);
			const page = new URL(this.env.location);
			if (url.origin !== page.origin || !["http:", "https:"].includes(url.protocol) || url.username || url.password || url.hash) {
				throw new Error("Archive URLs must use the same origin as this page.");
			}
			return url;
		}

		async initialize() {
			try {
				this.discoveryURL = this.sameOriginURL(this.root.dataset.discoveryUrl);
				this.discoveryURL.searchParams.set("camli.mode", "config");
				this.archiveURL = this.sameOriginURL(this.root.dataset.archiveUrl);
				this.accountBase = this.sameOriginURL(this.root.dataset.accountBase);
				if (!this.accountBase.pathname.endsWith("/") || this.accountBase.search) {
					throw new Error("Invalid archive account URL base.");
				}
				if ((this.accountRef && !validRef(this.accountRef)) || (this.fileRef && !validRef(this.fileRef))) {
					throw new Error("Invalid archive account or file reference.");
				}
				this.setFileRef(this.fileRef);
				this.nodes["upload-form"].hidden = this.accountPage;
				this.render();
				if (this.accountPage) {
					this.showAccountLink(this.accountRef);
					if (this.fileRef) {
						await this.pollStatus();
					} else {
						this.phase = "empty";
						this.nodes.message.textContent = "No stored ZIP for this account. Upload another ZIP from the importer page.";
						this.render();
					}
				}
			} catch (error) {
				this.invalidConfiguration = true;
				this.phase = "failed";
				this.showError(error.message);
				this.nodes.upload.disabled = true;
				this.nodes.file.disabled = true;
			}
		}

		async config(signal) {
			if (!this.configuration) {
				const config = await this.request(this.discoveryURL, {signal});
				requireField(typeof config.authToken === "string" && Boolean(config.authToken.trim()) && !/[\r\n]/.test(config.authToken), "missing authToken");
				this.configuration = {authToken: config.authToken, uploadHelper: config.uploadHelper};
			}
			return this.configuration;
		}

		authHeaders(config) {
			return config.authToken === omitAuthToken ? {} : {Authorization: "Token " + config.authToken};
		}

		async request(url, options) {
			const response = await this.env.fetch(url.href, Object.assign({
				credentials: "same-origin",
				redirect: "error",
				cache: "no-store",
			}, options, {headers: Object.assign({Accept: "application/json"}, options && options.headers)}));
			return parseReply(await response.text(), response.status);
		}

		async api(action, signal) {
			const config = await this.config(signal);
			signal.throwIfAborted();
			return this.request(this.archiveURL, {
				method: "POST",
				signal,
				headers: Object.assign({"Content-Type": "application/json"}, this.authHeaders(config)),
				body: JSON.stringify({action, fileRef: this.fileRef}),
			});
		}

		async perform(phase, task, success, retryLabel, retryAction) {
			if (this.busy || this.disposed || this.invalidConfiguration || this.phase === "accepted") {
				return false;
			}
			this.clearTimer();
			this.phase = phase;
			this.busy = true;
			this.retry = null;
			this.clearError();
			const serial = ++this.serial;
			this.abortController = new AbortController();
			this.render();
			let ok = false;
			try {
				const value = await task(this.abortController.signal);
				if (this.disposed || serial !== this.serial) {
					return false;
				}
				success(value);
				ok = true;
			} catch (error) {
				if (this.disposed || serial !== this.serial) {
					return false;
				}
				if (error.authFailure) {
					this.configuration = null;
				}
				if (phase === "status") {
					this.lastStatus = null;
					this.nodes.progress.hidden = true;
					this.nodes.warnings.hidden = true;
				}
				if (error.inputChanged) {
					this.phase = "superseded";
					this.nodes.message.textContent = "A newer ZIP is now associated with this account. Monitoring stopped. Reload account to view its status.";
					this.retry = {label: "Reload account", action: () => this.env.reload()};
				} else {
					this.phase = "failed";
					const labels = {upload: "Upload", inspect: "Inspection", start: "Starting import", status: "Status check"};
					this.showError(labels[phase] + " failed: " + boundedText(error.message || "Network request failed."));
					this.retry = {label: retryLabel, action: retryAction};
					if (phase === "status") {
						this.nodes.message.textContent = "Status monitoring paused. Retry the status check to resume.";
					}
				}
			} finally {
				if (!this.disposed && serial === this.serial) {
					this.busy = false;
					this.abortController = null;
					this.xhr = null;
					this.render();
				}
			}
			return ok;
		}

		async upload() {
			if (this.busy || this.disposed || this.invalidConfiguration || this.accountPage || this.phase === "accepted") {
				return;
			}
			if (this.fileRef) {
				return this.inspect();
			}
			const files = this.nodes.file.files;
			if (!files || files.length !== 1 || files[0].size <= 0 || files[0].size > maxFileSize) {
				this.showError("Choose one non-empty ZIP, no larger than 32 GiB.");
				return;
			}
			const file = files[0];
			this.nodes.message.textContent = "Uploading ZIP…";
			this.nodes.progress.hidden = false;
			this.nodes.progress.removeAttribute("value");
			const ok = await this.perform("upload", async signal => {
				const config = await this.config(signal);
				signal.throwIfAborted();
				const helper = this.sameOriginURL(config.uploadHelper);
				return this.uploadFile(helper, config, file);
			}, ref => {
				this.setFileRef(ref);
				this.phase = "uploaded";
				this.nodes.progress.hidden = true;
			}, "Retry upload", () => this.upload());
			if (ok) {
				await this.inspect();
			}
		}

		uploadFile(url, config, file) {
			return new Promise((resolve, reject) => {
				const xhr = this.env.createXHR();
				const serial = this.serial;
				this.xhr = xhr;
				xhr.open("POST", url.href, true);
				xhr.setRequestHeader("Accept", "application/json");
				for (const [key, value] of Object.entries(this.authHeaders(config))) {
					xhr.setRequestHeader(key, value);
				}
				xhr.upload.onprogress = event => {
					if (this.disposed || serial !== this.serial) {
						return;
					}
					if (event.lengthComputable && event.total > 0) {
						this.nodes.progress.value = Math.min(1, event.loaded / event.total);
						this.nodes.message.textContent = event.loaded >= event.total ? "Storing ZIP…" : "Uploading ZIP…";
					} else {
						this.nodes.progress.removeAttribute("value");
					}
				};
				xhr.upload.onload = () => {
					if (!this.disposed && serial === this.serial) {
						this.nodes.message.textContent = "Storing ZIP…";
					}
				};
				xhr.onload = () => {
					try {
						if (xhr.responseURL) {
							this.sameOriginURL(xhr.responseURL);
						}
						const reply = parseReply(xhr.responseText, xhr.status);
						requireField(Array.isArray(reply.got) && reply.got.length === 1 && object(reply.got[0]) && validRef(reply.got[0].fileref), "expected exactly one uploaded file reference");
						resolve(reply.got[0].fileref);
					} catch (error) {
						reject(error);
					}
				};
				xhr.onerror = () => reject(new Error("Network failure while uploading ZIP."));
				xhr.ontimeout = () => reject(new Error("ZIP upload timed out."));
				xhr.onabort = () => reject(new Error("ZIP upload canceled."));
				const form = new FormData();
				form.append("modtime", new Date(file.lastModified).toISOString());
				form.append("ui-upload-file-helper-form", file);
				xhr.send(form);
			});
		}

		async inspect() {
			if (!this.fileRef || this.accountPage || this.busy || this.disposed || this.phase === "accepted") {
				return;
			}
			this.nodes.message.textContent = "Inspecting stored ZIP…";
			await this.perform("inspect", signal => this.api("inspect", signal), result => {
				validateIdentity(result.identity);
				requireField(result.accountRef === undefined || result.accountRef === "" || validRef(result.accountRef), "invalid matched account reference");
				const account = result.accountRef || "";
				if (result.status !== undefined && result.status !== null) {
					validateStatus(result.status, account, this.fileRef);
				}
				this.inspected = true;
				this.confirmed = false;
				this.inspectedAccount = account;
				this.nodes.identity.textContent = ["userName", "email", "name"]
					.filter(key => result.identity[key]).map(key => key + ": " + boundedText(result.identity[key])).join(" · ");
				this.nodes["account-match"].textContent = account ?
					"Import will reuse existing account " + account + "." : "Import will create a new account.";
				if (account) {
					this.showAccountLink(account);
				} else {
					this.nodes["account-link-container"].hidden = true;
				}
				this.phase = "confirm";
				this.nodes.message.textContent = "ZIP inspected. Review the identity and click Import archive to begin." +
					(result.status ? " Previous import: " + result.status.state + "." : "");
			}, "Retry inspection", () => this.inspect());
		}

		async start() {
			if (!validRef(this.fileRef) || !this.confirmed || this.busy || this.disposed || this.phase === "accepted") {
				return;
			}
			this.nodes.message.textContent = "Starting import of stored ZIP…";
			await this.perform("start", signal => this.api("start", signal), result => {
				validateIdentity(result.identity);
				requireField(validRef(result.accountRef), "missing started account reference");
				const expectedAccount = this.accountPage ? this.accountRef : this.inspectedAccount;
				requireField(!expectedAccount || result.accountRef === expectedAccount, "started account reference does not match");
				validateStatus(result.status, result.accountRef, this.fileRef);
				this.accountRef = result.accountRef;
				this.showAccountLink(this.accountRef);
				this.phase = "accepted";
				this.retry = null;
				this.nodes.message.textContent = "Import accepted. Opening account details…";
				// An accepted start must not become an automatic retry if navigation
				// fails. The durable account link remains usable instead.
				try {
					this.env.navigate(this.accountURL(this.accountRef));
				} catch (_) {
					this.nodes.message.textContent = "Import accepted. Open Account details to see its status.";
				}
			}, "Retry start of stored ZIP", () => this.start());
		}

		async pollStatus() {
			if (!this.accountPage || !this.fileRef || this.disposed || this.phase === "superseded") {
				return;
			}
			const ok = await this.perform("status", async signal => {
				const config = await this.config(signal);
				signal.throwIfAborted();
				const url = new URL(this.archiveURL);
				url.searchParams.set("account", this.accountRef);
				return this.request(url, {signal, headers: this.authHeaders(config)});
			}, status => {
				validateStatus(status, this.accountRef, "");
				if (!this.initialStatus && status.fileRef !== this.fileRef) {
					const error = new Error("A newer archive input replaced the monitored ZIP.");
					error.inputChanged = true;
					throw error;
				}
				this.initialStatus = false;
				this.setFileRef(status.fileRef);
				this.lastStatus = status;
				this.showStatus(status);
				this.phase = status.state === "running" ? "status" : "terminal";
				if (status.state !== "running") {
					this.retry = {
						label: status.state === "complete" ? "Reimport ZIP" : "Retry import of stored ZIP",
						action: () => {
							this.confirmed = true;
							this.start();
						},
					};
				}
			}, "Retry status check", () => this.pollStatus());
			if (ok && !this.disposed && this.lastStatus.state === "running") {
				this.timer = this.env.setTimeout(() => {
					this.timer = null;
					this.pollStatus();
				}, 1000);
			}
		}

		showStatus(status) {
			const progress = status.progress;
			const labels = {running: "Import running", complete: "Import complete", error: "Import failed", interrupted: "Import interrupted"};
			this.nodes.message.textContent = labels[status.state] + ": " + progress.itemsDone + " / " + progress.itemsTotal +
				" items; " + progress.itemsUnchanged + " unchanged; " + progress.warningCount + " warnings.";
			if (status.error) {
				this.showError(boundedText(status.error));
			}
			this.nodes.progress.hidden = false;
			this.nodes.progress.max = Math.max(1, progress.itemsTotal);
			if (progress.itemsTotal > 0 || status.state !== "running") {
				this.nodes.progress.value = progress.itemsDone;
			} else {
				this.nodes.progress.removeAttribute("value");
			}
			this.nodes.warnings.textContent = "";
			const warnings = progress.warnings || [];
			for (const warning of warnings.slice(0, 100)) {
				const item = document.createElement("li");
				item.textContent = warning.slice(0, 1024);
				this.nodes.warnings.appendChild(item);
			}
			if (progress.warningCount > Math.min(warnings.length, 100)) {
				const more = document.createElement("li");
				more.textContent = "Additional warnings: " + (progress.warningCount - Math.min(warnings.length, 100)) + ".";
				this.nodes.warnings.appendChild(more);
			}
			this.nodes.warnings.hidden = this.nodes.warnings.childElementCount === 0;
		}

		accountURL(ref) {
			return this.sameOriginURL(this.accountBase.href + encodeURIComponent(ref)).href;
		}

		showAccountLink(ref) {
			this.nodes["account-link"].href = this.accountURL(ref);
			this.nodes["account-link-container"].hidden = false;
		}

		setFileRef(ref) {
			this.fileRef = ref;
			this.nodes["file-ref"].textContent = ref;
			this.nodes["file-info"].hidden = !ref;
		}

		showError(message) {
			this.nodes.error.textContent = message;
			this.nodes.error.hidden = false;
		}

		clearError() {
			this.nodes.error.textContent = "";
			this.nodes.error.hidden = true;
		}

		render() {
			const locked = this.busy || this.disposed || this.invalidConfiguration || this.phase === "accepted";
			this.nodes.file.disabled = locked;
			this.nodes.upload.disabled = locked;
			this.nodes.cancel.hidden = !this.busy || !["upload", "inspect"].includes(this.phase);
			this.nodes.cancel.textContent = this.phase === "inspect" ? "Cancel inspection" : "Cancel upload";
			this.nodes.confirm.hidden = !this.inspected;
			this.nodes.start.disabled = locked || this.phase !== "confirm";
			this.nodes.retry.hidden = !this.retry;
			this.nodes.retry.disabled = locked;
			this.nodes.retry.textContent = this.retry ? this.retry.label : "";
			if (this.nodes.pause) {
				this.nodes.pause.disabled = !this.lastStatus || this.lastStatus.state !== "running" || this.phase === "start" || this.disposed;
			}
		}

		cancel() {
			if (!this.busy || !["upload", "inspect"].includes(this.phase)) {
				return;
			}
			const phase = this.phase;
			++this.serial;
			this.abortController.abort();
			if (this.xhr) {
				this.xhr.abort();
			}
			this.abortController = null;
			this.xhr = null;
			this.busy = false;
			this.phase = "idle";
			this.nodes.progress.hidden = true;
			this.nodes.message.textContent = phase === "inspect" ? "Inspection canceled. The stored ZIP is retained." : "Upload canceled.";
			this.retry = this.fileRef ?
				{label: "Retry inspection", action: () => this.inspect()} :
				{label: "Retry upload", action: () => this.upload()};
			this.render();
		}

		clearTimer() {
			if (this.timer !== null) {
				this.env.clearTimeout(this.timer);
				this.timer = null;
			}
		}

		dispose() {
			this.disposed = true;
			++this.serial;
			this.clearTimer();
			if (this.abortController) {
				this.abortController.abort();
			}
			if (this.xhr) {
				this.xhr.abort();
			}
			window.removeEventListener("pagehide", this.onPageHide);
			window.removeEventListener("pageshow", this.onPageShow);
			this.render();
		}
	}

	window.PerkeepArchiveImport = ArchiveImport;
	function initialize() {
		const root = document.getElementById("archive-import");
		if (root) {
			new ArchiveImport(root);
		}
	}
	if (document.readyState === "loading") {
		document.addEventListener("DOMContentLoaded", initialize, {once: true});
	} else {
		initialize();
	}
})();
