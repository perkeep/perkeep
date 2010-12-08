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

use constant CAMLI_SIGNER => "sha1-82e6f3494f698aa498d5906349c0aa0a183d89a6";

my $j = JSON::Any->new;
my $json = $j->objToJson({ "camliVersion" => 1,
                           "camliSigner" => CAMLI_SIGNER,
                           "foo" => "bar",
                         });

# Sign it.
my $sjson;
{
    my $req = req("sign", { "json" => $json });
    my $res = $ua->request($req);
    ok($res, "got an HTTP sig response") or done_testing();
    ok($res->is_success, "HTTP sig response is successful") or done_testing();
    $sjson = $res->content;
    print "Got signed: $sjson";
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
    print "Verify response: " . $res->content;
    my $vobj = $j->jsonToObj($res->content);
    ok(defined($vobj->{'signatureValid'}), "has 'signatureValid' key");
    ok($vobj->{'signatureValid'}, "signature is valid");
    my $vdat = $vobj->{'verifiedData'};
    ok(defined($vdat), "has verified data");
    is($vdat->{'camliSigner'}, CAMLI_SIGNER, "signer matches");
    is($vdat->{'foo'}, "bar")
}

# Verification that should fail.
{
    my $req = req("verify", { "sjson" => "{}" });
    my $res = $ua->request($req);
    ok($res, "got an HTTP verify response") or done_testing();
    ok($res->is_success, "HTTP verify response is successful") or done_testing();
    print "Verify response: " . $res->content;
    my $vobj = $j->jsonToObj($res->content);
    ok(defined($vobj->{'signatureValid'}), "has 'signatureValid' key");
    is(0, $vobj->{'signatureValid'}, "signature is properly invalid");
    ok(!defined($vobj->{'verifiedData'}), "no verified data key");
    ok(defined($vobj->{'errorMessage'}), "has an error message");
}

# Imposter!  Verification should fail.
{
    my $eviljson = q{{"camliVersion":1,"camliSigner":"sha1-82e6f3494f698aa498d5906349c0aa0a183d89a6","foo":"evilbar","camliSig":"iQEcBAABAgAGBQJM+tnUAAoJEIUeCLJL7Fq1ruwH/RplOpmrTK51etXUHayRGN0RM0Jxttjwa0pPuiHr7fJifaZo2pvMZOMAttjFEP/HMjvpSVi8P7awBFXXlCTj0CAlexsmCsPEHzITXe3siFzH+XCSmfHNPYYti0apQ2+OcWNnzqWXLiEfP5yRVXxcxoWuxYlnFu+mfw5VdjrJpIa+n3Ys5D4zUPVCSNtF4XV537czqfd9AiSfKCY/aL2NuZykl4WtP3JgYl8btE84EjNLFasQDstcWOvp7rrP6T8hQQotw5/F4SmmFM6ybkWXk/Wkax3XpzW9qL00VqhxHd4JIWaSzSV/WcSQwCoLWc7uXttOWgVtMIhzpjeMlqt1gc0==QYU2"}};
    my $req = req("verify", { "sjson" => $eviljson });
    my $res = $ua->request($req);
    ok($res, "got an HTTP verify response") or done_testing();
    ok($res->is_success, "HTTP verify response is successful") or done_testing();
    print "Verify response: " . $res->content;
    my $vobj = $j->jsonToObj($res->content);
    ok(defined($vobj->{'signatureValid'}), "has 'signatureValid' key");
    is(0, $vobj->{'signatureValid'}, "signature is properly invalid");
    ok(!defined($vobj->{'verifiedData'}), "no verified data key");
    ok(defined($vobj->{'errorMessage'}), "has an error message");
    like($vobj->{'errorMessage'}, qr/bad signature: RSA verification error/, "verification error");
}

done_testing(29);

sub req {
    my ($method, $post_params) = @_;
    return POST($server->root . "/camli/sig/" . $method,
                "Authorization" => "Basic dGVzdDp0ZXN0", # test:test
                Content => $post_params);
}
