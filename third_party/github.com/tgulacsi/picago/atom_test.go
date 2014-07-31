// Copyright 2014 Tamás Gulácsi. All rights reserved.
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package picago

import (
	"encoding/xml"
	"os"
	"reflect"
	"testing"
	"time"
)

const albumsXML = `<?xml version='1.0' encoding='utf-8'?>
<feed xmlns='http://www.w3.org/2005/Atom'
    xmlns:openSearch='http://a9.com/-/spec/opensearch/1.1/'
    xmlns:exif='http://schemas.google.com/photos/exif/2007'
    xmlns:geo='http://www.w3.org/2003/01/geo/wgs84_pos#'
    xmlns:gml='http://www.opengis.net/gml'
    xmlns:georss='http://www.georss.org/georss'
    xmlns:batch='http://schemas.google.com/gdata/batch'
    xmlns:media='http://search.yahoo.com/mrss/'
    xmlns:gphoto='http://schemas.google.com/photos/2007'
    xmlns:gd='http://schemas.google.com/g/2005'
    gd:etag='W/"CkABRXY8fip7ImA9WxVVGE8."'>
  <id>https://picasaweb.google.com/data/feed/api/user/liz</id>
  <updated>2009-03-12T01:19:14.876Z</updated>
  <category scheme='http://schemas.google.com/g/2005#kind'
    term='http://schemas.google.com/photos/2007#user' />
  <title>liz</title>
  <subtitle></subtitle>
  <icon>https://iconPath/liz.jpg</icon>
  <link rel='http://schemas.google.com/g/2005#feed'
    type='application/atom+xml'
    href='https://picasaweb.google.com/data/feed/api/user/liz' />
  <link rel='http://schemas.google.com/g/2005#post'
    type='application/atom+xml'
    href='https://picasaweb.google.com/data/feed/api/user/liz' />
  <link rel='alternate' type='text/html'
    href='http://picasaweb.google.com/liz' />
  <link rel='http://schemas.google.com/photos/2007#slideshow'
    type='application/x-shockwave-flash'
    href='http://picasaweb.google.com/s/c/bin/slideshow.swf?host=picasaweb.google.com&amp;RGB=0x000000&amp;feed=https%3A%2F%2Fpicasaweb.google.com%2Fdata%2Ffeed%2Fapi%2Fuser%2Fliz%3Falt%3Drss' />
  <link rel='self' type='application/atom+xml'
    href='https://picasaweb.google.com/data/feed/api/user/liz?start-index=1&amp;max-results=1000&amp;v=2' />
  <author>
    <name>Liz</name>
    <uri>http://picasaweb.google.com/liz</uri>
  </author>
  <generator version='1.00' uri='http://picasaweb.google.com/'>
    Picasaweb</generator>
  <openSearch:totalResults>1</openSearch:totalResults>
  <openSearch:startIndex>1</openSearch:startIndex>
  <openSearch:itemsPerPage>1000</openSearch:itemsPerPage>
  <gphoto:user>liz</gphoto:user>
  <gphoto:nickname>Liz</gphoto:nickname>
  <gphoto:thumbnail>
    https://thumbnailPath/liz.jpg</gphoto:thumbnail>
  <gphoto:quotalimit>1073741824</gphoto:quotalimit>
  <gphoto:quotacurrent>32716</gphoto:quotacurrent>
  <gphoto:maxPhotosPerAlbum>500</gphoto:maxPhotosPerAlbum>
  <entry gd:etag='"RXY8fjVSLyp7ImA9WxVVGE8KQAE."'>
    <id>https://picasaweb.google.com/data/entry/api/user/liz/albumid/albumID</id>
    <published>2005-06-17T07:09:42.000Z</published>
    <updated>2009-03-12T01:19:14.000Z</updated>
    <app:edited xmlns:app='http://www.w3.org/2007/app'>
      2009-03-12T01:19:14.000Z</app:edited>
    <category scheme='http://schemas.google.com/g/2005#kind'
      term='http://schemas.google.com/photos/2007#album' />
    <title>lolcats</title>
    <summary>Hilarious Felines</summary>
    <rights>public</rights>
    <link rel='http://schemas.google.com/g/2005#feed'
      type='application/atom+xml'
      href='https://picasaweb.google.com/data/feed/api/user/liz/albumid/albumID?authkey=authKey&amp;v=2' />
    <link rel='alternate' type='text/html'
      href='http://picasaweb.google.com/lh/album/aFDUU2eJpMHZ1dP5TGaYHxtMTjNZETYmyPJy0liipFm0?authkey=authKey' />
    <link rel='self' type='application/atom+xml'
      href='https://picasaweb.google.com/data/entry/api/user/liz/albumid/albumID?authkey=authKey&amp;v=2' />
    <link rel='edit' type='application/atom+xml'
      href='https://picasaweb.google.com/data/entry/api/user/liz/albumid/albumID/1236820754876000?authkey=authKey&amp;v=2' />
    <link rel='http://schemas.google.com/acl/2007#accessControlList'
      type='application/atom+xml'
      href='https://picasaweb.google.com/data/entry/api/user/liz/albumid/albumID/acl?authkey=authKey&amp;v=2' />
    <author>
      <name>Liz</name>
      <uri>http://picasaweb.google.com/liz</uri>
    </author>
    <gphoto:id>albumID</gphoto:id>
    <gphoto:location>Mountain View, CA</gphoto:location>
    <gphoto:access>public</gphoto:access>
    <gphoto:timestamp>1118992182000</gphoto:timestamp>
    <gphoto:numphotos>1</gphoto:numphotos>
    <gphoto:numphotosremaining>499</gphoto:numphotosremaining>
    <gphoto:bytesUsed>23044</gphoto:bytesUsed>
    <gphoto:user>liz</gphoto:user>
    <gphoto:nickname>Liz</gphoto:nickname>
    <media:group>
      <media:title type='plain'>lolcats</media:title>
      <media:description type='plain'>Hilarious
        Felines</media:description>
      <media:keywords></media:keywords>
      <media:content url='https://imagePath/Lolcats.jpg' type='image/jpeg' medium='image' />
      <media:thumbnail url='https://thumbnailPath/Lolcats.jpg' height='160' width='160' />
      <media:credit>Liz</media:credit>
    </media:group>
  </entry>
</feed>`

const photosXML = `<?xml version='1.0' encoding='utf-8'?>
<feed xmlns='http://www.w3.org/2005/Atom'
    xmlns:app='http://www.w3.org/2007/app'
    xmlns:exif='http://schemas.google.com/photos/exif/2007'
    xmlns:georss='http://www.georss.org/georss'
    xmlns:gml='http://www.opengis.net/gml'
    xmlns:gphoto='http://schemas.google.com/photos/2007'
    xmlns:media='http://search.yahoo.com/mrss/'
    xmlns:openSearch='http://a9.com/-/spec/opensearch/1.1/'
    xmlns:gd='http://schemas.google.com/g/2005'
    gd:etag='W/"A0MBR347eCp7ImA9WxRbFkQ."'>
  <id>https://picasaweb.google.com/data/feed/user/liz/albumid/albumID</id>
  <updated>2008-12-08T01:24:16.000Z</updated>
  <category scheme='http://schemas.google.com/g/2005#kind'
    term='http://schemas.google.com/photos/2007#album' />
  <title>lolcats</title>
  <subtitle>Hilarious Felines</subtitle>
  <rights>public</rights>
  <icon>https://iconPath/Lolcats.jpg</icon>
  <link rel='http://schemas.google.com/g/2005#feed'
    type='application/atom+xml'
    href='https://picasaweb.google.com/data/feed/api/user/liz/albumid/albumID' />
  <link rel='http://schemas.google.com/g/2005#post'
    type='application/atom+xml'
    href='https://picasaweb.google.com/data/feed/api/user/liz/albumid/albumID' />
  <link rel='alternate' type='text/html'
    href='http://picasaweb.google.com/liz/Lolcats' />
  <link rel='http://schemas.google.com/photos/2007#slideshow'
    type='application/x-shockwave-flash'
    href='http://picasaweb.google.com/s/c/bin/slideshow.swf?host=picasaweb.google.com&amp;RGB=0x000000&amp;feed=https%3A%2F%2Fpicasaweb.google.com%2Fdata%2Ffeed%2Fapi%2Fuser%2Fliz%2Falbumid%2FalbumID%3Falt%3Drss' />
  <link rel='http://schemas.google.com/photos/2007#report'
    type='text/html'
    href='http://picasaweb.google.com/lh/reportAbuse?uname=liz&amp;aid=albumID' />
  <link rel='http://schemas.google.com/acl/2007#accessControlList'
    type='application/atom+xml'
    href='https://picasaweb.google.com/data/feed/api/user/liz/albumid/albumID/acl' />
  <link rel='self' type='application/atom+xml'
    href='https://picasaweb.google.com/data/feed/api/user/liz/albumid/albumID?start-index=1&amp;max-results=1000&amp;v=2' />
  <author>
    <name>Liz</name>
    <uri>http://picasaweb.google.com/liz</uri>
  </author>
  <generator version='1.00' uri='http://picasaweb.google.com/'>
    Picasaweb</generator>
  <openSearch:totalResults>1</openSearch:totalResults>
  <openSearch:startIndex>1</openSearch:startIndex>
  <openSearch:itemsPerPage>1000</openSearch:itemsPerPage>
  <gphoto:id>albumID</gphoto:id>
  <gphoto:location>Mountain View, CA, USA</gphoto:location>
  <gphoto:access>public</gphoto:access>
  <gphoto:timestamp>1150527600000</gphoto:timestamp>
  <gphoto:numphotos>1</gphoto:numphotos>
  <gphoto:numphotosremaining>499</gphoto:numphotosremaining>
  <gphoto:bytesUsed>23044</gphoto:bytesUsed>
  <gphoto:user>liz</gphoto:user>
  <gphoto:nickname>Liz</gphoto:nickname>
  <georss:where>
    <gml:Point>
      <gml:pos>37.38911780598221 -122.08638668060303</gml:pos>
    </gml:Point>
    <gml:Envelope>
      <gml:lowerCorner>37.38482151758655 -122.0958924293518</gml:lowerCorner>
      <gml:upperCorner>37.39341409437787 -122.07688093185425</gml:upperCorner>
    </gml:Envelope>
  </georss:where>
  <gphoto:allowPrints>true</gphoto:allowPrints>
  <gphoto:allowDownloads>true</gphoto:allowDownloads>
  <entry gd:etag='"Qns7fDVSLyp7ImA9WxRbFkQCQQI."'>
    <id>http://picasaweb.google.com/data/entry/user/liz/albumid/albumID/photoid/photoID</id>
    <published>2008-08-15T18:58:44.000Z</published>
    <updated>2008-12-08T01:11:03.000Z</updated>
    <app:edited>2008-12-08T01:11:03.000Z</app:edited>
    <category scheme='http://schemas.google.com/g/2005#kind'
      term='http://schemas.google.com/photos/2007#photo' />
    <title type='text'>invisible_bike.jpg</title>
    <summary type='text'>Bike</summary>
    <content type='image/jpeg'
      src='http://photoPath/invisible_bike.jpg' />
    <link rel='http://schemas.google.com/g/2005#feed'
      type='application/atom+xml'
      href='https://picasaweb.google.com/data/feed/api/user/liz/albumid/albumID/photoid/photoID' />
    <link rel='alternate' type='text/html'
      href='http://picasaweb.google.com/liz/Lolcats#photoID' />
    <link rel='http://schemas.google.com/photos/2007#canonical'
      type='text/html'
      href='http://picasaweb.google.com/lh/photo/THdOPB27qGrofntiI91-8w' />
    <link rel='self' type='application/atom+xml'
      href='https://picasaweb.google.com/data/entry/api/user/liz/albumid/albumID/photoid/photoID' />
    <link rel='edit' type='application/atom+xml'
      href='https://picasaweb.google.com/data/entry/api/user/liz/albumid/albumID/photoid/photoID' />
    <link rel='edit-media' type='image/jpeg'
      href='https://picasaweb.google.com/data/media/api/user/liz/albumid/albumID/photoid/photoID' />
    <link rel='http://schemas.google.com/photos/2007#report'
      type='text/html'
      href='http://picasaweb.google.com/lh/reportAbuse?uname=liz&amp;aid=albumID&amp;iid=photoID' />
    <gphoto:id>photoID</gphoto:id>
    <gphoto:position>1.66002086E9</gphoto:position>
    <gphoto:albumid>albumID</gphoto:albumid>
    <gphoto:access>public</gphoto:access>
    <gphoto:width>410</gphoto:width>
    <gphoto:height>295</gphoto:height>
    <gphoto:size>23044</gphoto:size>
    <gphoto:client />
    <gphoto:checksum />
    <gphoto:timestamp>1218826724000</gphoto:timestamp>
    <gphoto:commentCount>0</gphoto:commentCount>
    <exif:tags>
      <exif:imageUniqueID>0657130896bace739a44ce90a7d5b451</exif:imageUniqueID>
    </exif:tags>
    <media:group>
      <media:content url='https://photoPath/invisible_bike.jpg'
        height='295' width='410' type='image/jpeg' medium='image' />
      <media:credit>Liz</media:credit>
      <media:description type='plain'></media:description>
      <media:keywords>invisible, bike</media:keywords>
      <media:thumbnail url='https://thumbnailPath/s72/invisible_bike.jpg'
        height='52' width='72' />
      <media:thumbnail url='https://thumbnailPath/s144/invisible_bike.jpg'
        height='104' width='144' />
      <media:thumbnail url='https://thumbnailPath/s288/invisible_bike.jpg'
        height='208' width='288' />
      <media:title type='plain'>invisible_bike.jpg</media:title>
    </media:group>
    <georss:where>
      <gml:Point>
        <gml:pos>37.427399548633325 -122.1703290939331</gml:pos>
      </gml:Point>
      <gml:Envelope>
        <gml:lowerCorner>37.42054944692195 -122.1825385093689</gml:lowerCorner>
        <gml:upperCorner>37.4342496503447 -122.15811967849731</gml:upperCorner>
      </gml:Envelope>
    </georss:where>
  </entry>
</feed>`

func TestAtom(t *testing.T) {
	for _, text := range []string{albumsXML, photosXML} {
		var result Atom

		if err := xml.Unmarshal([]byte(text), &result); err != nil {
			t.Errorf("Unmarshal error: %v", err)
		}
		t.Logf("result: %#v", result)
	}
}

func mustParseAtom(t *testing.T, file string) *Atom {
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	a := new(Atom)
	if err := xml.NewDecoder(f).Decode(a); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestVideoInGallery(t *testing.T) {
	atom := mustParseAtom(t, "testdata/gallery-with-a-video.xml")
	if len(atom.Entries) != 3 {
		t.Fatalf("num entries = %d; want 3", len(atom.Entries))
	}
	p, err := atom.Entries[2].photo()
	if err != nil {
		t.Fatal(err)
	}
	if p.Type != "video/mpeg4" {
		t.Errorf("type = %q; want video/mpeg4", p.Type)
	}
	if got, want := p.URL, "https://foo.googlevideo.com/bar.mp4"; got != want {
		t.Errorf("URL = %q; want %q", got, want)
	}
	if got, want := p.PageURL, "https://picasaweb.google.com/114403741484702971746/BikingWithBlake#6041225428268790466"; got != want {
		t.Errorf("PageURL = %q; want %q", got, want)
	}
	wantKW := []string{"keyboard", "stuff"}
	if !reflect.DeepEqual(p.Keywords, wantKW) {
		t.Errorf("Keywords = %q; want %q", p.Keywords, wantKW)
	}
}

func TestAlbumFromEntry(t *testing.T) {
	atom := mustParseAtom(t, "testdata/album-list.xml")
	if len(atom.Entries) != 3 {
		t.Fatalf("num entries = %d; want 3", len(atom.Entries))
	}
	var got []Album
	for _, ent := range atom.Entries {
		got = append(got, ent.album())
	}
	tm := func(s string) time.Time {
		ret, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return ret
	}
	want := []Album{
		Album{ID: "6040139514831220113", Name: "BikingWithBlake", Title: "Biking with Blake", Description: "Description is biking up San Bruno mountain.\n\nAnd a newline.", Location: "San Bruno Mt, CA", AuthorName: "Gast Erson", AuthorURI: "https://picasaweb.google.com/114403741484702971746", Published: tm("2014-07-22T07:00:00.000Z"), Updated: tm("2014-07-28T22:22:25.577Z"), URL: "https://picasaweb.google.com/114403741484702971746/BikingWithBlake"},
		Album{ID: "6041693388376552305", Name: "Mexico", Title: "Mexico", Description: "", Location: "", AuthorName: "Gast Erson", AuthorURI: "https://picasaweb.google.com/114403741484702971746", Published: tm("2014-07-30T03:36:00.000Z"), Updated: tm("2014-07-30T19:46:05.346Z"), URL: "https://picasaweb.google.com/114403741484702971746/Mexico"},
		Album{ID: "6041709940397032273", Name: "TestingOver2048", Title: "testing over 2048", Description: "", Location: "", AuthorName: "Gast Erson", AuthorURI: "https://picasaweb.google.com/114403741484702971746", Published: tm("2014-07-30T04:40:14.000Z"), Updated: tm("2014-07-30T05:01:02.919Z"), URL: "https://picasaweb.google.com/114403741484702971746/TestingOver2048"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d", len(got), len(want))
	}
	if reflect.DeepEqual(got, want) {
		return
	}
	for i := range got {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("index %d doesn't match:\n got: %+v\nwant: %+v\n", i, got[i], want[i])
		}
	}
}
