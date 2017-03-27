/*
Copyright 2017 The Camlistore Authors.

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

package main

var rootHTML = `
<!DOCTYPE html PUBLIC "-//W3C//DTD HTML 4.01//EN"
   "http://www.w3.org/TR/html4/strict.dtd">

<html lang="en">
<head>
	<meta http-equiv="Content-Type" content="text/html; charset=utf-8">
	<title>Scanning Cabinet</title>
	<base href="{{.BaseURL}}">
	<link rel="stylesheet" type="text/css" href="{{.BaseURL}}ui/scanner.css" />
{{ if .AllTags }}
	<link rel="stylesheet" type="text/css" href="https://visapi-gadgets.googlecode.com/svn/trunk/wordcloud/wc.css"/>
	<script type="text/javascript" src="https://www.google.com/jsapi"></script>
{{ end }}

</head>
<body>
    {{ if .TopMessage }}
      <center><span style='background: #ffc; padding: 0.5em'>{{.TopMessage}}</span></center>
    {{ end }}
    {{ if .ErrorMessage }}
      <center><span style='background: #ffc; padding: 0.5em; color: red'>{{.ErrorMessage}}</span></center>
    {{ end }}
  <div>[<a href='{{.BaseURL}}'>Scanning Cabinet</a>]</div>


<h2>Search</h2>
<form method='GET' id='tagform'>
<div>Tag search: <input type='text' size='50' name='tags' id='taginput' value='{{.Tags}}' /> <input type='submit' value='Search' /> (comma-separated union)</div>
</form>

{{ if .Media }}
<h2>Un-annotated raw scans</h2>
    <form method='POST' action='makedoc' />
    <input type='submit' value='Make doc from selected' />
    <div id='scans'>
    {{ range .Media }}
      <div style='margin: 1em; float:left; height: auto'>
        <div style='display: block'>
          <input type='checkbox' id='check_{{.BlobRef}}' name="blobref" value="{{.BlobRef}}" />
          [<a target=_blank href="{{.UrlResize}}800">larger</a>]<br/>
          <label for='check_{{.BlobRef}}'><img src="{{.ThumbUrl}}" class="doc-page-row" /></label>
        </div>
      </div>
	{{ end }}
    </div> <!-- scans -->
    </form>
    <br clear='both' />
{{ end }}

{{ if .SearchedDocs }}
<h2>Search Results</h2>
    <ul>
    {{ range .SearchedDocs }}
      <li><b>
        <a href="{{.DisplayUrl}}">{{.SomeTitle}}</a>
      </b> [{{.DateYyyyMmDd}}]{{ if .Description }} ({{.Description}}){{ end }}
      </li>
    {{ end }}
    </ul>
{{ end }}

{{ if .AllTags }}
<h2>All Documents Tags</h2>
<div id="wcdiv"></div>

	  <script type="text/javascript" src="https://visapi-gadgets.googlecode.com/svn/trunk/wordcloud/wc.js"></script>
	  <script type="text/javascript" src="https://ajax.googleapis.com/ajax/libs/jquery/1.9.0/jquery.min.js"></script>
	<script type="text/javascript">
		google.load("visualization", "1");
		google.setOnLoadCallback(draw);
		function draw() {
			var data = new google.visualization.DataTable();
			data.addColumn('string', 'Text1');
			data.addRows({{ len .AllTags }});
			{{ range $index, $tags := .AllTags }}
			data.setCell({{ $index }}, 0, {{ $tags }});
			{{ end }}
			var outputDiv = document.getElementById('wcdiv');
			var wc = new WordCloud(outputDiv);
			wc.draw(data, null);
			attachEvents();
		}
		function attachEvents() {
			$("div.word-cloud span")
				.css('cursor', 'pointer')
				.click(function () {
					$("input#taginput").val($(this).text());
					$("form#tagform").submit();
				});
		}
	</script>
{{ end }}

<!---- Upcoming due documents --->
{{ if .UpcomingDocs }}
<h2>Upcoming Due Documents</h2>
    <ul>
    {{ range .UpcomingDocs }}
      <li><b>{{.DueYyyyMmDd}}</b> &#8212;
        <a href="{{.DisplayUrl}}">{{.SomeTitle}}</a>
      </b> {{ if .Description }} ({{.Description}}){{ end }}
      </li>
    {{ end }}
    </ul>
{{ end }}
<!---- Upcoming due documents --->


<!---- Docs without tags --->
{{ if .UntaggedDocs }}
<h2>Untagged Documents</h2>
    <ul>
    {{ range .UntaggedDocs }}
      <li><b>
        <a href="{{.DisplayUrl}}">{{.SomeTitle}}</a>
      </b> {{ if .Description }} ({{.Description}}){{ end }}
      </li>
    {{ end }}
    </ul>
{{ end }}
<!---- /Docs without tags --->

</body>
</html>
`

var docHTML = `
<!DOCTYPE html PUBLIC "-//W3C//DTD HTML 4.01//EN"
   "http://www.w3.org/TR/html4/strict.dtd">

<html lang="en">
<head>
	<meta http-equiv="Content-Type" content="text/html; charset=utf-8">
	<title>Scanning Cabinet</title>
	<base href="{{.BaseURL}}">
	<link rel="stylesheet" type="text/css" href="{{.BaseURL}}ui/scanner.css" />
</head>
<body>
  <div>[<a href='{{.BaseURL}}'>Scanning Cabinet</a>]</div>


<h2><img src="http://www.gstatic.com/codesite/ph/images/star_off.gif" width=15 height=15 />
    {{ if .Doc.Title}}
       {{.Doc.Title| html}}
    {{ else }}
       Document {{.Doc.BlobRef}}
    {{ end }}
</h2>
<form method='POST' action='changedoc'>
<input type='hidden' name='docref' value='{{.Doc.BlobRef}}' />
<table>
  <tr><td align='right'>Title</td><td><input name='title' value="{{.Doc.Title| html}}" size=80 /></td></tr>
  <tr><td align='right'>Tags</td><td><input name='tags' value="{{.Doc.TagCommaSeparated | html}}" size=80/></td></tr>
  <tr><td align='right'>Doc Date</td><td><input name='date' value="{{.Doc.DateYyyyMmDd}}" maxlength=10 /> (yyyy-mm-dd)</td></tr>
  <tr><td align='right'>Due Date</td><td><input name='due_date' value="{{.Doc.DueYyyyMmDd}}" maxlength=10 /> (yyyy-mm-dd)</td></tr>
  <tr><td align='right'>Location</td>
      <td><input name='physical_location'
           value="{{ if .Doc.PhysicalLocation }}{{.Doc.PhysicalLocation | html}}{{ end }}" size=60 />
           (of physical document)</td>
  </tr>
  <tr>
    <td></td>
    <td><input type='submit' value="Save" />
      Other action: <select name='mode'>
        <option value="">(other options)</option>
        <option value="break">Break; delete doc, keep images</option>
        <option value="delete">Delete; delete doc &amp; images</option>
      </select>
    </td>
  </tr>
</table>
</form>

{{ $ize := .Size }}
{{ if .ShowSingleList }}
    <center>
	{{ range .Pages }}
		<img src="{{.UrlResize}}{{$ize}}" class="doc-page-single" /><br />
	{{ end }}
	</center>
{{ else }}
	{{ range .Pages }}
		<img src="{{.UrlResize}}{{$ize}}" class="doc-page-row" />
	{{ end }}
{{ end }}
`
