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
                           "camliSigner" => "sha1-82e6f3494f698aa498d5906349c0aa0a183d89a6",
                           "foo" => "bar",
                         });

print "JSON: [$json]\n";
my $keyid = "26F5ABDA";  # test key


# Sign it.
my $sjson;
{
    my $req = req("sign", { "json" => $json, "keyid" => $keyid });
    my $res = $ua->request($req);
    ok($res, "got an HTTP sig response") or done_testing();
    ok($res->is_success, "HTTP sig response is successful") or done_testing();
    $sjson = $res->content;
    diag("Got signed: $sjson");
    like($sjson, qr/camliSig/, "contains camliSig substring");
    
    my $sobj = $j->jsonToObj($sjson);
    is($sobj->{"foo"}, "bar", "key foo is still bar");
    is($sobj->{"camliVersion"}, 1, "key camliVersion is still 1");
    ok(defined $sobj->{"camliSig"}, "has camliSig key");
    ok(defined $sobj->{"camliSigner"}, "has camliSigner key");
    is(scalar keys %$sobj, 4, "total of 3 keys in signed object");
}

# Verify it.
{
    my $req = req("verify", { "sjson" => $sjson });
    my $res = $ua->request($req);
    ok($res, "got an HTTP verify response") or done_testing();
    ok($res->is_success, "HTTP verify response is successful") or done_testing();
    my $vcontent = $res->content;
}

done_testing(9);

sub req {
    my ($method, $post_params) = @_;
    return POST($server->root . "/camli/sig/" . $method,
                "Authorization" => "Basic dGVzdDp0ZXN0", # test:test
                Content => $post_params);
}
