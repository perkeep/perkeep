/*
Copyright 2012 Google Inc.

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

package server

const topWizard = `
<!doctype html>
<html>
<head>
	<title>Camlistore setup</title>
</head>
<body>
	<p>[<a href="/">Back</a>]</p>
	<h1>Setup Wizard</h1>
	<p> See <a href="http://camlistore.org/docs/server-config">Server Configuration</a> for information on configuring the values below.</p>
	<form id="WizardForm" action="" method="post" enctype="multipart/form-data">
`

const bottomWizard = `
</body>
</html>
`
