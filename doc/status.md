<h1>Status</h1>

<p>Camlistore has <a href="/docs/arch">many pieces</a> and many <a
href="/docs/uses">potential use cases</a>.  Here are some of the
pieces and use cases, and where they're at.</p>

<p>Last updated: <b>2013-02-02</b> (updated rarely. Ask the mailing list or see the git commit logs)</p>

<h2>Roadmap</h2>

<p>See the <a href="https://docs.google.com/document/d/1xBJ2Oovj1sqzksMRVVSnb-N524kOPl1efJT_svikxVY/edit">2012-10-20 Camlistore Roadmap</a> document.</p>

<p>See the <a href="/">home page</a> for the latest release's release notes.</p>

<h2>Specification</h2>

<table class='status'>
<tr><th>Item</th><th>Status</th><th>Notes</th></tr>
<tr><td>Blob Server</td><td>95%</td><td>See <a href="/gw/doc/protocol">doc/protocol/</a></td></tr>
<tr><td>Sharing</td><td>50%</td><td>See mailing list thread "Example of sharing"</td></tr>
<tr><td>Search API</td><td>0%</td><td>initial hand waving only</td></tr>
</table>


<h2>Servers</h2>
<table class='status'>
<tr><th>Item</th><th>Status</th><th>Notes</th></tr>
<tr><td>Server</td><td>95%</td><td>See <a href="/gw/server/camlistored">server/camlistored</a>. Written in Go, runs on Linux, OS X, Windows (sometimes regresses), and App Engine (some optimizations / and ease-of-setup remain). Does the blobserver, UI, search, sharing, etc.</td></tr>

<tr><td>Search / indexer server</td><td>95%</td><td>Good framework now, and usable. Runs on either memory, SQLite, MySQL, Postgres, MongoDB, or App Engine.  More things need to be indexed always, but we keep adding more, and it's easy. It's a library used by the server above.</td></tr>

<tr><td>Blob Server test suite</td><td>80%</td><td>See <a href="/gw/server/tester/bs-test.pl">server/tester/bs-test.pl</a>. Unmaintained. Mostly use Go's unit tests against the Go server.</td></tr>

</table>

<h2>Tools</h2>

<table class='status'>
<tr><th>Item</th><th>Status</th><th>Notes</th></tr>

<tr><td><a href="/cmd/camput">camput</a></td><td>99%</td><td>the kitchen sink tool to inject content into a blobserver. Quite good now. Also <a href="https://plus.google.com/u/0/115863474911002159675/posts/DWmyygSrvt7">used by the Android client</a>, as a child process.</td></tr>

<tr><td><a href="/cmd/camget">camget</a></td><td>10%</td><td>tool to retrieve content from a blobserver.</td></tr>

<tr><td><a href="/cmd/cammount">cammount</a></td><td>read-only</td><td>Read-only FUSE mounting support, for mounting backups. Works on Linux and OS X. We have a plan for read-write.</td></tr>

<tr><td><a href="/gw/clients/android">Android Uploader</a></td><td>90%</td><td>UI is kinda ugly in spots but it works and
optionally backs up your SD card (photos, etc) to your blob server. Uses camput.
Can also work in "Share with Camli" mode, one resource at a
time.</td></tr>

<tr><td><a href="/gw/clients/chrome/clip-it-good">Clip It Good</a></td><td>80%</td>

<td>Camli port of <a href="https://chrome.google.com/extensions/detail/aimbcenmfmipmcllpgajennfdfmmhmjj">Clip It Good</a>. Chrome extension allows right-click on images and save them to your blobserver.  (currently still forked)</td></tr>

<tr><td><a href="/gw/clients/python">camliclient.py</a></td><td>50%</td><td>example command-line tool for uploading/downloading files using the <a href="/gw/lib/python">Python library</a>.</td></tr>

</table>

<h2>High-Level Use Cases</h2>
<p>... TODO: table for the various <a href="/docs/uses">use cases</a>.</p>

