#!/usr/bin/perl

use strict;
my $file = shift;
die "Usage: test-put <file> [base_url]" unless -f $file;
my $sha1 = `sha1sum $file`;
$sha1 =~ s!\s.*!!s;

my $url = shift;
$url ||= "http://127.0.0.1:3179";
$url =~ s!/$!!;

# Bogus sha1:
#$sha1 = "f1d2d2f924e986ac86fdf7b36c94bcdf32beec15";

$url .= "/camli/sha1-$sha1";

system("curl", "-T", $file, $url) and die "Curl failed.";


