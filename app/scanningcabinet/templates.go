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
	<script src="{{.BaseURL}}ui/scanner.js"></script>
	<link rel="stylesheet" href="{{.BaseURL}}ui/jQCloud.css">
	<script type="text/javascript" src="{{.BaseURL}}ui/jquery.min.js"></script>
	<script src="{{.BaseURL}}ui/jQCloud.js"></script>
	<link rel="stylesheet" href="{{.BaseURL}}ui/jquery-ui.css">
	<script src="{{.BaseURL}}ui/jquery-ui.min.js"></script>

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
      </b> [{{.DateYyyyMmDd}}]
      {{ $minusTags := .Tags.Minus $.Tags }}
      {{ if $minusTags }}
        ({{ range $i, $tag := $minusTags }}{{if $i}}, {{end}}<a href="?tags={{$tag}}">{{$tag}}</a>{{ end }})
      {{ end }}
      </li>
    {{ end }}
    </ul>
{{ end }}

<h2>All Documents Tags</h2>
<div id="wcdiv" style="height: 600px; width: 600px;"></div>
<script type="text/javascript">
$(document).ready(function(){
  // A frequency map of all existing tags
  var allTags = {{ .AllTags }};

  enableAutoComplete(allTags, "input#taginput");

  $('#wcdiv').jQCloud($.map(allTags, function(freq, tag){
    return {
      text: tag,
      weight: freq,
      html: { class: "jqcloud-word" },
      handlers: { click: (function(tag){
          return function() { window.location.href = "?tags=" + tag; }
        })(tag)
      }
    };
  }));
})
</script>

<!---- Upcoming due documents --->
{{ if .UpcomingDocs }}
<h2>Upcoming Due Documents</h2>
    <ul>
    {{ range .UpcomingDocs }}
      <li><b>{{.DueYyyyMmDd}}</b> &#8212;
        <a href="{{.DisplayUrl}}">{{.SomeTitle}}</a>
      </b>
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
      </b>
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
	<script src="{{.BaseURL}}ui/scanner.js"></script>

	<script type="text/javascript" src="{{.BaseURL}}ui/jquery.min.js"></script>
	<link rel="stylesheet" href="{{.BaseURL}}ui/jquery-ui.css">
	<script src="{{.BaseURL}}ui/jquery-ui.min.js"></script>
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
  <tr><td align='right'>Tags</td><td><input id="tags" name='tags' value="{{.Doc.Tags | html}}" size=80/></td></tr>
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

<script type="text/javascript">

// A frequency map of all existing tags
var allTags = {{ .AllTags }};

$(document).ready(function(){
  enableAutoComplete(allTags, "input#tags");
});
</script>
`
