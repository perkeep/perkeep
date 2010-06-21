#!/usr/bin/perl
#
# Test script to run against a Camli blobserver to test its compliance
# with the spec.

use strict;
use Getopt::Long;
use LWP;
use Test::More;

my $user;
my $password;
GetOptions("user" => \$user,
           "password" => \$password) or usage();

usage() unless @ARGV == 1;
my $hostport = shift;

sub usage {
    die "Usage: bs-test.pl [--user= --password=] <host:port>\n";
}

die "TODO: implement";
