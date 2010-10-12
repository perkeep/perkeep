#!/usr/bin/perl
#
# This is a basic indexing system for prototyping and demonstration
# purposes only.
#
# It's not production quality by any stretch of the imagination: it
# uses sqlite, is single-threaded, single-process, etc.
#

use strict;
use DBI;
use DBD::SQLite;
use Getopt::Long;
use LWP::UserAgent;
use LWP::ConnCache;
use JSON::Any;
use Net::Netrc;

my $blobserver;
my $dbname;
GetOptions(
    "server=s" => \$blobserver,
    "dbfile=s" => \$dbname,
    ) or usage();

usage() unless $blobserver && $blobserver =~ m!^(?:(https?)://)?([^/]+)!;
my $scheme = $1 || "http";
my $hostport = $2;

sub usage {
    die
        "Usage: index.pl --server=[http|https://]hostname[:port]\n" .
        "                --dbfile=<filename>   (defaults to hostname_port.camlidx.db)\n";
}

my $netrc_mach = Net::Netrc->lookup($hostport)
    or die "No ~/.netrc file or section for host '$hostport'.\n";

unless ($dbname) {
    $dbname = "$hostport.camlidx.db";
    $dbname =~ s/:/_/;
}
my $needs_init = ! -f $dbname || -s $dbname == 0;
my $db = DBI->connect("dbi:SQLite:dbname=$dbname", "", "", { RaiseError => 1 })
    or die "Failed to open sqlite file $dbname";
if ($needs_init) {
    $db->do("CREATE TABLE blobs (" .
            "   blobref   VARCHAR(80) NOT NULL PRIMARY KEY, " .
            "   size      INT NULL, " .
            "   mimetype  VARCHAR(30) NULL)");
}

my $ua = LWP::UserAgent->new(
    agent => "Camlistore/BasicIndexer",
    conn_cache => LWP::ConnCache->new,
    );

my $json = JSON::Any->new;

# TODO: remove hard-coded realm
$ua->credentials($hostport, "camlistored", "user", $netrc_mach->password);

print "Iterating over blobs.\n";
my $n_blobs = learn_blob_digests_and_sizes();
print "Number of blobs: $n_blobs.\n";

sub learn_blob_digests_and_sizes {
    my $after = "";
    my $n_blobs = 0;
    while (1) {
        my $after_display = $after || "(start)";
        print "Enumerating starting at: $after_display ... ($n_blobs blobs so far)\n";
        my $res = $ua->get("$scheme://$hostport/camli/enumerate-blobs?after=$after&limit=1000");
        unless ($res->is_success) {
            die "Failure from /camli/enumerate-blobs?after=$after: " . $res->status_line;
        }
        my $jres = $json->jsonToObj($res->content);

        my $bloblist = $jres->{'blobs'};
        if (ref($bloblist) eq "ARRAY") {
            my $first = $bloblist->[0]{'blobRef'};
            my $last = $bloblist->[-1]{'blobRef'};
            my $sth = $db->prepare("SELECT blobref, size, mimetype FROM blobs WHERE " .
                                   "blobref >= ? AND blobref <= ?");
            $sth->execute($first, $last);
            my %inventory;  # blobref -> [$size, $bool_have_mime]
            while (my ($lblob, $lsize, $lmimetype) = $sth->fetchrow_array) {
                $inventory{$lblob} = [$lsize, defined($lmimetype)];
            }
            foreach my $blob (@$bloblist) {
                $n_blobs++;
                my $lblob = $inventory{$blob->{'blobRef'}};
                if (!$lblob) {
                    print "Inserting $blob->{'blobRef'} ...\n";
                    $db->do("INSERT INTO blobs (blobref, size) VALUES (?, ?)", undef,
                            $blob->{'blobRef'}, $blob->{'size'});
                    next;
                }
                if ($lblob && !$lblob->[0] && $blob->{'size'}) {
                    print "Updating size of $blob->{'blobRef'} ...\n";
                    $db->do("UPDATE blobs SET size=? WHERE blobref=?", undef,
                            $blob->{'size'}, $blob->{'blobRef'});
                    next;
                }
            }
        }

        last unless $jres->{'after'} && $jres->{'after'} gt $after;
        $after = $jres->{'after'};
    }
    return $n_blobs;
}
