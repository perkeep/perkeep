#!/usr/bin/perl

use strict;
use Test::More;
use FindBin;
use lib "$FindBin::Bin";
use CamsigdTest;

my $server = CamsigdTest::start();

ok($server, "Started the server") or BAIL_OUT("can't start the server");

my $ua = LWP::UserAgent->new;
my $req = HTTP::Request->new("GET", $server->root . "/");
my $res = $ua->request($req);
ok($res, "got an HTTP response") or done_testing();
ok($res->is_success, "HTTP response is successful");

done_testing(3);

