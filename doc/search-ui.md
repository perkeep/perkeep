# Search UI

The User Interface's "Search" box accepts predicates of the form
"[-]operator:value[:value]".  These predicates may be separated by 'and' or 'or'
keywords, or spaces which mean the same as 'and'. Expressions like this may be
grouped with parenthesis. Grouped expressions are evaluated first. Grouped
expressions may be negated.

An 'and' besides an 'or' is evaluated first. This means for example that

    tag:foo or is:pano tag:bar

will return all images having tag foo together with the panorama images having
tag bar.

Negation of a predicate is achieved by prepending a minus sign: -is:landscape
will match with pictures of not landscape ratio. For example:

    -(after:"2010-01-01" before:"2010-03-02T12:33:44") or loc:"Amsterdam"

will return all images having "modtime" outside the specified period, joined
with all images taken in Amsterdam.

The logical grouping of the **map** predicate is an exception, see its definition.

When you need to match a value containing a space, you need to use double quotes
around the value only. For example: tag:"Three word tagname" and not "tag:Three
word tagname".  If your value contains double quotes you can use backslash
escaping.  For example:

    attr:bar:"He said: \"Hi\""

## Usable operators

**<a name="after"></a>after**
: date format is RFC3339, but can be shortened as required.

**<a name="attr"></a>attr**
: match on attribute. Use attr:foo:bar to match nodes having their foo attribute
  set to bar, or attr:foo:~bar to match nodes whose foo attribute contains bar
  (case insensitive substring match).

**<a name="before"></a>before**
: i.e. `2011-01-01` is Jan 1 of year 2011, and `2011` means the same.

**<a name="childrenof"></a>childrenof**
: Find child permanodes of a parent permanode (or prefix of a parent permanode):
  `childrenof:sha1-527cf12`

**<a name="filename"></a>filename**
: search for permanodes of files with this filename (case sensitive)

**<a name="format"></a>format**
: file's format (or MIME-type) such as jpg, pdf, tiff.

**<a name="location"></a>has:location**
: image has a location (GPSLatitude and GPSLongitude can be retrieved from the
  image's EXIF tags).

**<a name="height"></a>height**
: use `height:min-max` to match images having a height of at least min and at most
  max. Use `height:min-` to specify only an underbound and `height:-max` to specify
  only an upperbound.  Exact matches should use `height:480`

**<a name="image"></a>is:image**
: object is an image

**<a name="lanscape"></a>is:landscape**
: the image has a landscape aspect

**<a name="like"></a>is:like**
: the object is a liked tweet

**<a name="pano"></a>is:pano**
: the image is panoramic: its width to height ratio is greater than or equal to 2.0.

**<a name="portrait"></a>is:portrait**
: the image has a portrait aspect.

**<a name="loc"></a>loc**
: uses the available metadata, such as EXIF GPS fields, or check-in locations,
  to match nodes having a location near the specified location.  Locations are
  resolved using maps.googleapis.com. For example: `loc:"new york, new york"`
  This requires that you get a Geocoding API key from Google.
  See the the page on [how to configure geocoding](/doc/geocoding.md).

**<a name="locrect"></a>locrect**
: uses the various location metadata fields (such as EXIF GPS) to match nodes
  having a location within the specified location area. The area is defined by
  its North-West corner, followed and comma-separated by its South-East corner.
  Each corner is defined by its latitude, followed and comma-separated by its
  longitude. For example: `locrect:48.63,-123.37,46.59,-121.28`

**<a name="map"></a>map**
: is defined like locrect, and it has a similar meaning. However, it is not
  defined server-side, and it is specifically meant to represent the area of the
  world that is visible in the screen when using the map aspect, and in particular
  when zooming or panning. As such, it follows stricter rules than the other
  predicates, which are:
  1. only one map predicate is allowed in the whole expression.
  2. since the map predicate is interpreted as if it were a logical 'and' with the
     rest of whole expression (regardless of its position within the expression),
     logical 'or's around it are forbidden.

**<a name="parentof"></a>parentof**
: Find parent permanodes of a child permanode (or prefix of a child permanode):
  `parentof:sha1-527cf12`

**<a name="raw"></a>raw**
: matches the given JSON [search constraint](https://perkeep.org/pkg/search#Constraint).<br>
  `raw:{"permanode": {"attr": "camliContent", "valueInSet": {"file": {"mediaTag": {"tag": "title", "string": {"hasPrefix": "Bohemian"}}}}}}`

**<a name="ref"></a>ref**
: matches nodes whose blobRef starts with the given substring:
  `ref:sha1-527cf12`

**<a name="tag"></a>tag**
: match on a tag

**<a name="width"></a>width**
: use width:min-max to match images having a width of at least min and at most
  max. Use width:min- to specify only an underbound and width:-max to specify
  only an upperbound.  Exact matches should use `width:640`
