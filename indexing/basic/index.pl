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
            "   headbytes VARCHAR(1024) NULL, ".
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
my $n_blobs = populate_blob_digests_and_sizes();
print "Number of blobs: $n_blobs.\n";
populate_blob_types();

sub populate_blob_digests_and_sizes {
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

sub populate_blob_types {
    my $after = "";
    my $n_blobs = 0;
    while (1) {
        print "Querying for un-sniffed blobs after '$after'...\n";
        my $sth = $db->prepare("SELECT blobref, size, headbytes FROM blobs WHERE " .
                               "mimetype IS NULL AND size IS NOT NULL AND blobref > ? LIMIT 50");
        $sth->execute($after);
        my $cursor_count = 0;
        while (my ($blobref, $size, $headbytes) = $sth->fetchrow_array) {
            $after = $blobref;
            $cursor_count++;
            my $need_headbytes_update = 0;
            $headbytes = "" if defined($size) && $size == 0;
            if (defined $headbytes) {
                print "Unknown type for $blobref ...\n";
            } else {
                print "Fetching $blobref ...\n";
                my $req = HTTP::Request->new(GET => "$scheme://$hostport/camli/$blobref");
                $req->header("Range" => "bytes=0-1024");
                my $res = $ua->request($req);
                unless ($res->is_success) {
                    die "Failure fetching head of /camli/$blobref: " . $res->status_line;
                }
                $headbytes = $res->content;
                $need_headbytes_update = 1;
                my $size = length($headbytes);
                print "Fetching $blobref = $size byte header\n";
            }
            my $type = get_type_from_magic($headbytes);
            next unless $type or $need_headbytes_update;
            print "Type of $blobref: $type\n";
            $db->do("UPDATE blobs SET headbytes=?, mimetype=? WHERE blobref=?", undef,
                    $headbytes, $type, $blobref);
        }
        print "count: $cursor_count\n";
        last unless $cursor_count > 0;
    }
}

sub get_type_from_magic {
    my $magic = shift;
    if ($magic =~ /^{.+"camliVersion"/s) {
        return "application/json+camli";
    }
    if ($magic =~ /^\xff\xd8\xff\xe1/) {
        return "image/jpeg";
    }
    if ($magic =~ /^<\?xml\b.+<gpx\b/s) { # TODO: over-broad
        return "application/gpx+xml";  # not actually registered?
    }
    return undef;
}

