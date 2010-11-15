#!/usr/bin/perl

use strict;
use Test::More;
use FindBin;
use lib "$FindBin::Bin";
use CamsigdTest;

my $server = CamsigdTest::start();

ok($server);

done_testing(1);
