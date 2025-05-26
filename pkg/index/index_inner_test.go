/*
Copyright 2011 The Perkeep Authors

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

package index

import (
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/types/camtypes"
)

var testKvClaims = map[[2]string]camtypes.Claim{
	[2]string{"claim|sha224-eaf72715d32f498a4a3fe9f372495962d7298ebf018536a4f1f88221|2931A67C26F5ABDA|2011-11-28T01:32:37.000123456Z|sha224-60c6ad71a30be964f8d8a6f148a053af238de0d9c300bea7fe93fdf0", "set-attribute|tag|foo1|sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"}: camtypes.Claim{
		BlobRef: blob.MustParse("sha224-60c6ad71a30be964f8d8a6f148a053af238de0d9c300bea7fe93fdf0"),
		Signer:  blob.MustParse("sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"),
		Date:    timeParse("2011-11-28T01:32:37.000123456Z"),
		Type:    "set-attribute", Attr: "tag", Value: "foo1",
		Permanode: blob.MustParse("sha224-eaf72715d32f498a4a3fe9f372495962d7298ebf018536a4f1f88221"),
	},

	[2]string{"claim|sha224-eaf72715d32f498a4a3fe9f372495962d7298ebf018536a4f1f88221|2931A67C26F5ABDA|2011-11-28T01:32:38.000123456Z|sha224-9ca71b6ffbb46cda3d5cc4c1ef68ab3860cc2638febd1254cd4c5891", "set-attribute|tag|foo2|sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"}: camtypes.Claim{
		BlobRef: blob.MustParse("sha224-9ca71b6ffbb46cda3d5cc4c1ef68ab3860cc2638febd1254cd4c5891"),
		Signer:  blob.MustParse("sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"),
		Date:    timeParse("2011-11-28T01:32:38.000123456Z"),
		Type:    "set-attribute", Attr: "tag", Value: "foo2",
		Permanode: blob.MustParse("sha224-eaf72715d32f498a4a3fe9f372495962d7298ebf018536a4f1f88221"),
	},
	[2]string{"claim|sha224-eaf72715d32f498a4a3fe9f372495962d7298ebf018536a4f1f88221|2931A67C26F5ABDA|2011-11-28T01:32:39.000123456Z|sha224-79aca6e3cafa70ec76603a62acacf98722d561490664af378cdadef6", "set-attribute|camliRoot|rootval|sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"}: camtypes.Claim{
		BlobRef: blob.MustParse("sha224-79aca6e3cafa70ec76603a62acacf98722d561490664af378cdadef6"),
		Signer:  blob.MustParse("sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"),
		Date:    timeParse("2011-11-28T01:32:39.000123456Z"),
		Type:    "set-attribute", Attr: "camliRoot", Value: "rootval",
		Permanode: blob.MustParse("sha224-eaf72715d32f498a4a3fe9f372495962d7298ebf018536a4f1f88221"),
	},
	[2]string{"claim|sha224-eaf72715d32f498a4a3fe9f372495962d7298ebf018536a4f1f88221|2931A67C26F5ABDA|2011-11-28T01:32:42.000123456Z|sha224-755a6cb33d2dd321c9d84be933273dee704f7b49ad0de31a778c10bf", "add-attribute|camliMember|sha224-ebc479bcb179797980b20e7bc7946b0c74cec7c0aec253632f009d3a|sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"}: camtypes.Claim{
		BlobRef: blob.MustParse("sha224-755a6cb33d2dd321c9d84be933273dee704f7b49ad0de31a778c10bf"),
		Signer:  blob.MustParse("sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"),
		Date:    timeParse("2011-11-28T01:32:42.000123456Z"),
		Type:    "add-attribute", Attr: "camliMember", Value: "sha224-ebc479bcb179797980b20e7bc7946b0c74cec7c0aec253632f009d3a",
		Permanode: blob.MustParse("sha224-eaf72715d32f498a4a3fe9f372495962d7298ebf018536a4f1f88221"),
	},
	[2]string{"claim|sha224-eaf72715d32f498a4a3fe9f372495962d7298ebf018536a4f1f88221|2931A67C26F5ABDA|2011-11-28T01:32:43.000123456Z|sha224-00331f51dd359449e5ad95d42bbe3d770d5ed22e0894bb46a0bb2afb", "del-attribute|title|pony|sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"}: camtypes.Claim{
		BlobRef: blob.MustParse("sha224-00331f51dd359449e5ad95d42bbe3d770d5ed22e0894bb46a0bb2afb"),
		Signer:  blob.MustParse("sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"),
		Date:    timeParse("2011-11-28T01:32:43.000123456Z"),
		Type:    "del-attribute", Attr: "title", Value: "pony",
		Permanode: blob.MustParse("sha224-eaf72715d32f498a4a3fe9f372495962d7298ebf018536a4f1f88221"),
	},
	[2]string{"claim|sha224-56d600f010ef4c88ab6f3828f7b2e990fa12d5b76ad20b252ddcac91|2931A67C26F5ABDA|2011-11-28T01:32:37.000123456Z|sha224-dd2b8122938d91f22e1e680e1c0fd7bcba74c1e7b8e81943e38aa91c", "set-attribute|tag|foo1|sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"}: camtypes.Claim{
		BlobRef: blob.MustParse("sha224-dd2b8122938d91f22e1e680e1c0fd7bcba74c1e7b8e81943e38aa91c"),
		Signer:  blob.MustParse("sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"),
		Date:    timeParse("2011-11-28T01:32:37.000123456Z"),
		Type:    "set-attribute", Attr: "tag", Value: "foo1",
		Permanode: blob.MustParse("sha224-56d600f010ef4c88ab6f3828f7b2e990fa12d5b76ad20b252ddcac91"),
	},
	[2]string{"claim|sha224-56d600f010ef4c88ab6f3828f7b2e990fa12d5b76ad20b252ddcac91|2931A67C26F5ABDA|2011-11-28T01:32:38.000123456Z|sha224-ac330e06f01f8859a606ec9d994a4a908e464bf3f5f3d64aeb4d7622", "delete|||sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"}: camtypes.Claim{
		BlobRef: blob.MustParse("sha224-ac330e06f01f8859a606ec9d994a4a908e464bf3f5f3d64aeb4d7622"),
		Signer:  blob.MustParse("sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"),
		Date:    timeParse("2011-11-28T01:32:38.000123456Z"),
		Type:    "delete", Attr: "", Value: "",
		Permanode: blob.MustParse("sha224-56d600f010ef4c88ab6f3828f7b2e990fa12d5b76ad20b252ddcac91"),
	},
	[2]string{"claim|sha224-56d600f010ef4c88ab6f3828f7b2e990fa12d5b76ad20b252ddcac91|2931A67C26F5ABDA|2011-11-28T01:32:39.000123456Z|sha224-d52d4a25c0feb7c142f8d0adcc5f795a61122f0b1cc2ee65584d0495", "delete|||sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"}: camtypes.Claim{
		BlobRef: blob.MustParse("sha224-d52d4a25c0feb7c142f8d0adcc5f795a61122f0b1cc2ee65584d0495"),
		Signer:  blob.MustParse("sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"),
		Date:    timeParse("2011-11-28T01:32:39.000123456Z"),
		Type:    "delete", Attr: "", Value: "",
		Permanode: blob.MustParse("sha224-56d600f010ef4c88ab6f3828f7b2e990fa12d5b76ad20b252ddcac91"),
	},
	[2]string{"claim|sha224-56d600f010ef4c88ab6f3828f7b2e990fa12d5b76ad20b252ddcac91|2931A67C26F5ABDA|2011-11-28T01:32:37.000123456Z|sha224-dd2b8122938d91f22e1e680e1c0fd7bcba74c1e7b8e81943e38aa91c", "set-attribute|tag|foo1|sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"}: camtypes.Claim{
		BlobRef: blob.MustParse("sha224-dd2b8122938d91f22e1e680e1c0fd7bcba74c1e7b8e81943e38aa91c"),
		Signer:  blob.MustParse("sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"),
		Date:    timeParse("2011-11-28T01:32:37.000123456Z"),
		Type:    "set-attribute", Attr: "tag", Value: "foo1",
		Permanode: blob.MustParse("sha224-56d600f010ef4c88ab6f3828f7b2e990fa12d5b76ad20b252ddcac91"),
	},
	[2]string{"claim|sha224-56d600f010ef4c88ab6f3828f7b2e990fa12d5b76ad20b252ddcac91|2931A67C26F5ABDA|2011-11-28T01:32:38.000123456Z|sha224-ac330e06f01f8859a606ec9d994a4a908e464bf3f5f3d64aeb4d7622", "delete|||sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"}: camtypes.Claim{
		BlobRef: blob.MustParse("sha224-ac330e06f01f8859a606ec9d994a4a908e464bf3f5f3d64aeb4d7622"),
		Signer:  blob.MustParse("sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"),
		Date:    timeParse("2011-11-28T01:32:38.000123456Z"),
		Type:    "delete", Attr: "", Value: "",
		Permanode: blob.MustParse("sha224-56d600f010ef4c88ab6f3828f7b2e990fa12d5b76ad20b252ddcac91"),
	},
	[2]string{"claim|sha224-56d600f010ef4c88ab6f3828f7b2e990fa12d5b76ad20b252ddcac91|2931A67C26F5ABDA|2011-11-28T01:32:39.000123456Z|sha224-d52d4a25c0feb7c142f8d0adcc5f795a61122f0b1cc2ee65584d0495", "delete|||sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"}: camtypes.Claim{
		BlobRef: blob.MustParse("sha224-d52d4a25c0feb7c142f8d0adcc5f795a61122f0b1cc2ee65584d0495"),
		Signer:  blob.MustParse("sha224-a794846212ff67acdd00c6b90eee492baf674d41da8a621d2e8042dd"),
		Date:    timeParse("2011-11-28T01:32:39.000123456Z"),
		Type:    "delete", Attr: "", Value: "",
		Permanode: blob.MustParse("sha224-56d600f010ef4c88ab6f3828f7b2e990fa12d5b76ad20b252ddcac91"),
	},
}

func TestKvClaim(t *testing.T) {
	for in, claim := range testKvClaims {
		in, claim := in, claim
		t.Run(in[0]+","+in[1], func(t *testing.T) {
			c, ok := kvClaim(in[0], in[1], blob.Parse)
			if !ok {
				t.Errorf("got %t, wanted %t", ok, true)
			}
			if c != claim {
				t.Errorf("got %+v, wanted %+v", c, claim)
			}

			c2, ok := kvClaimBytes([]byte(in[0]), []byte(in[1]), blob.ParseBytes)
			if !ok {
				t.Errorf("got %t, wanted %t", ok, true)
			}
			if c2 != claim {
				t.Errorf("got %+v, wanted %+v", c2, claim)
			}
		})
	}
}

func BenchmarkKvClaim(b *testing.B) {
	type testCase struct {
		k, v  string
		claim camtypes.Claim
	}
	bb := make([]testCase, 0, len(testKvClaims))
	for k, v := range testKvClaims {
		bb = append(bb, testCase{k: k[0], v: k[1], claim: v})
	}
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		for _, tC := range bb {
			c, ok := kvClaim(tC.k, tC.v, blob.Parse)
			if !ok {
				b.Errorf("got %t, wanted %t", ok, true)
			}
			if c != tC.claim {
				b.Errorf("got %+v, wanted %+v", c, tC.claim)
			}
		}
	}
}

func BenchmarkKvClaimBytes(b *testing.B) {
	type testCase struct {
		k, v  []byte
		claim camtypes.Claim
	}
	bb := make([]testCase, 0, len(testKvClaims))
	for k, v := range testKvClaims {
		bb = append(bb, testCase{k: []byte(k[0]), v: []byte(k[1]), claim: v})
	}
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		for _, tC := range bb {
			c, ok := kvClaimBytes(tC.k, tC.v, blob.ParseBytes)
			if !ok {
				b.Errorf("got %t, wanted %t", ok, true)
			}
			if c != tC.claim {
				b.Errorf("got %+v, wanted %+v", c, tC.claim)
			}
		}
	}
}

func timeParse(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return t
}
