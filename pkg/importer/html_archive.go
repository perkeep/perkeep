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

package importer

// archiveUploadTmpl is the archiveUpload template, of the pages of
// importers that are ArchiveImporters.
const archiveUploadTmpl = `
{{define "archiveUpload"}}
<style>
.archive-import h2 {margin-top: 0;}
.archive-import button, .archive-import input {margin: .3em 0; font: inherit;}
.archive-import progress {width: 100%;}
.archive-import .archive-error {color: #b42318; white-space: pre-wrap;}
.archive-import .archive-ref {font-family: ui-monospace, SFMono-Regular, Menlo, monospace; overflow-wrap: anywhere;}
.archive-import ul {max-height: 20em; overflow: auto; white-space: pre-wrap;}
</style>
<section class="card archive-import" id="archive-import"
  data-discovery-url="{{.DiscoveryURL}}"
  data-archive-url="{{.ArchiveURL}}"
  data-account-base="{{.AccountBase}}"
  data-account-ref="{{.AccountRef}}"
  data-file-ref="{{.FileRef}}">
  <h2>{{if .AccountPage}}Archive import{{else}}Upload a {{.Title}} ZIP{{end}}</h2>
  <noscript>JavaScript is required to upload and import an archive.</noscript>
  {{if .AccountPage}}
    <p><a href="{{.AccountBase}}">Upload another ZIP</a>. Its identity will be checked before choosing an account.</p>
  {{else}}
    <p>Choose an account export ZIP. After uploading, check the detected identity and confirm the import.
       Existing accounts and activities are reused on later imports.</p>
  {{end}}
  <form id="archive-upload-form"{{if .AccountPage}} hidden{{end}}>
    <label for="archive-file">Account export ZIP</label><br>
    <input id="archive-file" name="archive" type="file" accept=".zip,application/zip" required><br>
    <button id="archive-upload" class="primary" type="submit" disabled>Upload ZIP</button>
    <button id="archive-cancel" type="button" hidden>Cancel upload</button>
  </form>
  <progress id="archive-progress" max="1" value="0" hidden></progress>
  <p id="archive-message" role="status" aria-live="polite"></p>
  <p id="archive-error" class="archive-error" role="alert" hidden></p>
  <p id="archive-file-info" hidden>Stored ZIP: <span id="archive-file-ref" class="archive-ref"></span>.
    Inspection and import retries reuse this file without uploading it again.</p>
  <div id="archive-confirm" hidden>
    <p id="archive-identity"></p>
    <p id="archive-account-match"></p>
    <button id="archive-start" class="primary" type="button">Import archive</button>
  </div>
  <p id="archive-account-link-container" hidden><a id="archive-account-link">Account details</a></p>
  <ul id="archive-warnings" hidden aria-label="Import warnings"></ul>
  <button id="archive-retry" type="button" hidden>Retry</button>
</section>
<script src="{{.ScriptURL}}" defer></script>
{{end}}
`
