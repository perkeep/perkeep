#!/usr/bin/perl

use strict;
use LWP::UserAgent;
use HTTP::Request;
use HTTP::Request::Common;
use Getopt::Long;

my $keyid = "26F5ABDA";
my $server = "http://localhost:2856";
GetOptions("keyid=s" => \$keyid,
           "server=s" => \$server)
    or usage();

$server =~ s!/$!!;

my $file = shift or usage();
-f $file or usage("$file isn't a file");

my $json = do { undef $/; open(my $fh, $file); <$fh> };

sub usage {
    my $err = shift;
    if ($err) {
        print STDERR "Error: $err\n";
    }
    print STDERR "Usage: client.pl [OPTS] <file.json>\n";
    print STDERR "Options:\n";
    print STDERR "   --keyid=<keyid>\n";
    print STDERR "   --server=http://host:port\n";
    exit(1);
}

my $req = POST("$server/camli/sig/sign",
               "Authorization" => "Basic dGVzdDp0ZXN0", # test:test
               Content => {
                   "json" => $json,
                   "keyid" => $keyid,
               });

my $ua = LWP::UserAgent->new;
my $res = $ua->request($req);
unless ($res->is_success) {
    die "Failure: " . $res->status_line . ": " . $res->content;
}

print $res->content;



