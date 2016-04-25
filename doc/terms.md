<h1>Terminology</h1>

<p>To stay sane and communicate effectively we try hard to use
consistent terminology throughout the pieces of the project.  Please let us know
if things here are confusing or lacking.</p>

<dl class='terms'>

<!-- ---------------------------------------------------------------------- -->
<dt id='blob'>blob</dt>

  <dd>an immutable sequence of 0 or more bytes, with no extra metadata</dd>

<!-- ---------------------------------------------------------------------- -->
<dt id='blobref'>blobref</dt>

  <dd>a reference to a blob, consisting of a cryptographic hash
  function name and that hash function's digest of the blob's bytes,
  in hex.  Examples of valid blobrefs include:
  <pre>
     sha1-f1d2d2f924e986ac86fdf7b36c94bcdf32beec15
     md5-d3b07384d113edec49eaa6238ad5ff00
     sha256-b5bb9d8014a0f9b1d61e21e796d78dccdf1352f23cd32812f4850b878ae4944c</pre>
  Concatenating the two together with a hyphen is the common
  representation, with both parts in all lower case.
  </dd>

<!-- ---------------------------------------------------------------------- -->
<dt id='blobserver'>blob server</dt>

  <dd>the simplest and lowest layer of the Camlistore servers (see: <a
     href="/docs/arch">architecture</a>).  A blob server, while
     potentially shared between users, is <em>logically private to a
     single user</em> and holds that user's blobs (<a
     href="/docs/schema">whatever they may represent</a>).

     <p>The protocol to speak with a blob server is simply:</p>
       <ul>

         <li><a
         href="/gw/doc/protocol/blob-get-protocol.txt"><b>get</b></a>
         a blob by its blobref.</li>

         <li><a
         href="/gw/doc/protocol/blob-upload-protocol.txt"><b>put</b></a>
         a blob by its blobref.</li>

         <li><a
         href="/gw/doc/protocol/blob-enumerate-protocol.txt"><b>enumerate</b></a>
         all your blobs, sorted by their blobrefs.  Enumeration is
         only really used by your search server and by a <em>full sync</em>
         between your blob server mirrors.</li>
        </ul>
      <p>(Note: no delete operation)</p>
   </dd>

<!-- ---------------------------------------------------------------------- -->
<dt id='schemablob'>schema blob</dt>

<dd>a <a href="/docs/schema">Camlistore-recognized data structure</a>, serialized as a JSON
object (map).  A schema blob must have top-level keys
<code>camliVersion</code> and <code>camliType</code> and start with a open brace (<code>{</code>, byte 0x7B).  You may use any valid JSON
serialization library to generate schema blobs.  Whitespace or formatting doesn't matter, as long as the blob
starts with <code>{</code> and is <a href="http://json.org/">valid JSON</a> in its entirety.

<p>Example:</p>
<pre class='sty'>
{
  "aKey": "itsValue",
  "camliType": "foo",
  "camliVersion": 1,
  "somethingElse": [1, 2, 3]
}</pre>

</dd>


<!-- ---------------------------------------------------------------------- -->
<dt id='claim'>signed schema blob (aka "claim")</dt>

<dd>if you <a href="/docs/json-signing">sign</a> a schema blob,
  it's now a "signed schema blob" or "claim".  The terms are used pretty
  interchangeably but generally it's called a <em>claim</em> when the target of
  the schema blob is an object's permanode (see below).

</dd>


<!-- ---------------------------------------------------------------------- -->
<dt id='object'>object</dt>

<dd>something that's mutable.  While a <em>blob</em> is a single
  immutable thing, an <em>object</em> is a collection of claims
  which mutate an object over time.  See <a href="#permanode" class='local'>permanode</a> for fuller discussion.
</dd>

<!-- ---------------------------------------------------------------------- -->
<dt id='permanode'>permanode</dt>

<dd>since an object is mutable and Camlistore is primarily content-addressed,
  the question arises how you could have a stable reference to something that's
  changing.  Camlistore solves this with the concept of a <em>permanode</em>.
  Like a permalink on the web, a permanode is a stable link to a Camli object.

  <p>A permanode is simply a <a href="/docs/json-signing">signed</a>
     schema blob with no data inside that would be interesting to
     mutate.  See <a href="/gw/doc/schema/objects/permanode.txt">the
     permanode spec</a>.</p>

  <p>A permanent reference to a mutable object then is simply the blobref of
     the permanode.</p>

  <p>The signer of a permanode is its owner. The search server and
     indexer will take this into account.  While multiple users may collaborate
     on mutating an object (by all creating new, signed mutation schema blobs),
     the owner ultimately decides the policies on how the mutations are respected.</p>

  <p>Example permanode blob:  (as generated with <code><a href="/cmd/camput">camput</a> --permanode</code>)</p>

     <pre class='sty' style="overflow: auto;">{"camliVersion": 1,
  "camliSigner": "sha1-c4da9d771661563a27704b91b67989e7ea1e50b8",
  "camliType": "permanode",
  "random": "HJ#/s#S+Q$rh:lHJ${)v"
,"camliSig":"iQEcBAABAgAGBQJNQzByAAoJEGjzeDN/6vt85G4IAI9HdygAD8bgz1BnRak6fI+L1dT56MxNsHyAoJaNjYJYKvWR4mrzZonF6l/I7SlvwV4mojofHS21urL8HIGhcMN9dP7Lr9BkCB428kvBtDdazdfN/XVfALVWJOuZEmg165uwTreMOUs377IZom1gjrhnC1bd1VDG7XZ1bP3PPxTxqppM0RuuFWx3/SwixSeWnI+zj9/Qon/wG6M/KDx+cCzuiBwwnpHf8rBmBLNbCs8SVNF3mGwPK0IQq/l4SS6VERVYDPlbBy1hNNdg40MqlJ5jr+Zln3cwF9WzQDznasTs5vK/ylxoXCvVFdOfwBaHkW1NHc3RRpwR0wq2Q8DN3mQ==gR7A"}</pre>

  </dd>

<!-- ---------------------------------------------------------------------- -->
<dt id='frontend'>frontend</dt>
<dd>the public-facing server that handles sharing and unified access to both
  your blob server and search server.  (see <a href="arch">architecture diagram</a>)
</dd>

<!-- ---------------------------------------------------------------------- -->
<dt id='fullsync'>full sync</dt>
<dd>synchronizing all your blobs between two or more of your blob servers
(e.g. mirroring between your house, App Engine, and Amazon).

<p>Generally a full sync will be done with the <em>blob server</em>'s enumerate
support and no knowledge of the schema.  It's a dumb copy of all blobs that the
other party doesn't already have.</p>
</dd>

<!-- ---------------------------------------------------------------------- -->
<dt id='graphsync'>graph sync</dt>

<dd>as opposed to a <em>full sync</em>, a graph sync is synchronizing
a sub-graph of your blobs between blob servers.  This level of sync will operate
with knowledge of the schema.</dd>

<!-- ---------------------------------------------------------------------- -->
<dt id='searchserver'>search server</dt>
<dt id='indexer'>indexer</dt>

<dd>TODO: finish documenting</dd>

</dl>

<script>
var terms = document.getElementsByTagName("dt");
for (var i = 0; i < terms.length; i++) {
  var term = terms[i];
  var id = term.getAttribute("id");
  if (!id) {
     continue;
  }
  var link = document.createElement("span");
  link.setAttribute("class", "termhashlink");
  link.innerHTML = "&nbsp;[<a href='#" + id + "'>#</a>]";
  term.appendChild(link);
}
</script>
