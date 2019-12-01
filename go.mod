module perkeep.org

go 1.12

require (
	bazil.org/fuse v0.0.0-20160811212531-371fbbdaa898
	cloud.google.com/go v0.49.0
	cloud.google.com/go/bigquery v1.3.0 // indirect
	cloud.google.com/go/datastore v1.0.0
	cloud.google.com/go/logging v1.0.0
	cloud.google.com/go/pubsub v1.1.0 // indirect
	cloud.google.com/go/storage v1.0.0
	github.com/FiloSottile/b2 v0.0.0-20170207175032-b197f7a2c317
	github.com/aws/aws-sdk-go v1.25.43
	github.com/bradfitz/latlong v0.0.0-20140711231157-b74550508561
	github.com/cznic/fileutil v0.0.0-20180108211300-6a051e75936f // indirect
	github.com/cznic/internal v0.0.0-20170905175358-4747030f7cf2 // indirect
	github.com/cznic/kv v0.0.0-20170515202733-892ccf731fb7
	github.com/cznic/lldb v1.1.0 // indirect
	github.com/cznic/mathutil v0.0.0-20180214153908-5455a562bccb // indirect
	github.com/cznic/sortutil v0.0.0-20150617083342-4c7342852e65 // indirect
	github.com/cznic/zappy v0.0.0-20160723133515-2533cb5b45cc // indirect
	github.com/edsrzf/mmap-go v0.0.0-20170320065105-0bce6a688712 // indirect
	github.com/facebookgo/ensure v0.0.0-20160127193407-b4ab57deab51 // indirect
	github.com/facebookgo/stack v0.0.0-20160209184415-751773369052 // indirect
	github.com/facebookgo/subset v0.0.0-20150612182917-8dac2c3c4870 // indirect
	github.com/garyburd/go-oauth v0.0.0-20180319155456-bca2e7f09a17
	github.com/go-ini/ini v1.38.1 // indirect
	github.com/go-sql-driver/mysql v1.4.1
	github.com/golang/groupcache v0.0.0-20191027212112-611e8accdfc9 // indirect
	github.com/golang/snappy v0.0.0-20170215233205-553a64147049 // indirect
	github.com/google/go-cmp v0.3.1 // indirect
	github.com/gopherjs/gopherjs v0.0.0-20181017120253-0766667cb4d1
	github.com/gopherjs/jquery v0.0.0-20180404123100-3ba2b901425e
	github.com/gorilla/websocket v1.4.1
	github.com/hashicorp/golang-lru v0.5.3 // indirect
	github.com/hjfreyer/taglib-go v0.0.0-20151027170453-0ef8bba9c41b
	github.com/jonas-p/go-shp v0.1.1 // indirect
	github.com/jstemmer/go-junit-report v0.9.1 // indirect
	github.com/kr/fs v0.1.0 // indirect
	github.com/lib/pq v1.2.0
	github.com/mailgun/mailgun-go v0.0.0-20171127222028-17e8bd11e87c
	github.com/mattn/go-mastodon v0.0.5-0.20190517015615-8f6192e26b66
	github.com/mattn/go-sqlite3 v1.13.0
	github.com/miekg/dns v0.0.0-20161003181808-3f1f7c8ec9ea
	github.com/nf/cr2 v0.0.0-20140528043846-05d46fef4f2f
	github.com/onsi/ginkgo v1.8.0 // indirect
	github.com/onsi/gomega v1.5.0 // indirect
	github.com/pkg/errors v0.8.0 // indirect
	github.com/pkg/sftp v0.0.0-20180419200840-5bf2a174b604
	github.com/plaid/plaid-go v0.0.0-20161222051224-02b6af68061b
	github.com/russross/blackfriday v2.0.0+incompatible
	github.com/rwcarlsen/goexif v0.0.0-20180518182100-8d986c03457a
	github.com/shurcooL/sanitized_anchor_name v1.0.0 // indirect
	github.com/smartystreets/goconvey v0.0.0-20190330032615-68dc04aab96a // indirect
	github.com/syndtr/goleveldb v0.0.0-20180608030153-db3ee9ee8931
	github.com/tgulacsi/picago v0.0.0-20171229130838-9e1ac2306c70
	go.opencensus.io v0.22.2 // indirect
	go4.org v0.0.0-20190218023631-ce4c26f7be8e
	golang.org/x/crypto v0.0.0-20190605123033-f99c8df09eb5
	golang.org/x/exp v0.0.0-20191129062945-2f5052295587 // indirect
	golang.org/x/image v0.0.0-20190802002840-cff245a6509b
	golang.org/x/lint v0.0.0-20191125180803-fdd1cda4f05f // indirect
	golang.org/x/net v0.0.0-20191126235420-ef20fe5d7933
	golang.org/x/oauth2 v0.0.0-20191122200657-5d9234df094c
	golang.org/x/sync v0.0.0-20190911185100-cd5d95a43a6e
	golang.org/x/sys v0.0.0-20191128015809-6d18c012aee9 // indirect
	golang.org/x/time v0.0.0-20190308202827-9d24e82272b4
	golang.org/x/tools v0.0.0-20191130070609-6e064ea0cf2d // indirect
	google.golang.org/api v0.14.0
	google.golang.org/appengine v1.6.5 // indirect
	google.golang.org/genproto v0.0.0-20191115221424-83cc0476cb11 // indirect
	google.golang.org/grpc v1.25.1 // indirect
	gopkg.in/check.v1 v1.0.0-20190902080502-41f04d3bba15 // indirect
	gopkg.in/ini.v1 v1.42.0 // indirect
	gopkg.in/mgo.v2 v2.0.0-20190816093944-a6b53ec6cb22
	rsc.io/pdf v0.0.0-20170302045715-1d34785eb915
	rsc.io/qr v0.2.0
)

replace google.golang.org/grpc v1.14.0 => github.com/bradfitz/grpc-go v0.0.0-20170203184515-188a132adcfba339f1f2d5da52498451341f9ee8
