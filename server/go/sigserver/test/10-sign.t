#!/usr/bin/perl

use strict;
use Test::More;
use FindBin;
use lib "$FindBin::Bin";
use CamsigdTest;
use JSON::Any;
use HTTP::Request::Common;

my $server = CamsigdTest::start();
ok($server, "Started the server") or BAIL_OUT("can't start the server");

my $ua = LWP::UserAgent->new;

my $j = JSON::Any->new;
my $json = $j->objToJson({ "camliVersion" => 1,
                           "foo" => "bar",
                         });

print "JSON: [$json]\n";
my $keyid = "26F5ABDA";  # test key

my $req = POST($server->root . "/camli/sig/sign",
               "Authorization" => "Basic dGVzdDp0ZXN0", # test:test
               Content => {
                   "json" => $json,
                   "keyid" => $keyid,
               });

my $res = $ua->request($req);
ok($res, "got an HTTP response") or done_testing();
ok($res->is_success, "HTTP response is successful") or done_testing();
my $sjson = $res->content;
diag("Got signed: $sjson");
like($sjson, qr/camliSig/, "contains camliSig substring");

my $sobj = $j->jsonToObj($sjson);
is($sobj->{"foo"}, "bar", "key foo is still bar");
is($sobj->{"camliVersion"}, 1, "key camliVersion is still 1");
ok(defined $sobj->{"camliSig"}, "has camliSig key");
is(scalar keys %$sobj, 3, "total of 3 keys in signed object");

done_testing(8);
