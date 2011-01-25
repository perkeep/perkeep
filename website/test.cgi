#!/usr/bin/perl
#

use strict;
print "Content-Type: text/html\n\n";

print "<html><head><title>dump output</title></head><body>\n";

if ($ENV{'REQUEST_METHOD'} eq "GET") {
    my $in = $ENV{'QUERY_STRING'};
    print "<h2>REQUEST_METHOD was GET</h2><pre>\n";
    print "Stdin= [$in]\n";
    print "</pre>\n";
} elsif ($ENV{'REQUEST_METHOD'} eq "POST") {
    my $in;
    sysread(STDIN, $in, $ENV{'CONTENT_LENGTH'});
    print "<h2>REQUEST_METHOD was POST</h2><pre>\n";
    print "Stdin= [$in]\n";
    print "</pre>\n";
} 

print "<h2>Environment variables</h2><pre>\n";
foreach my $key (sort(keys(%ENV))){
    print "<B>$key</B>", " "x(23-length($key)), "= $ENV{$key}\n";
}

print "</pre>\n";

print "</body></html>\n";
exit 0;
