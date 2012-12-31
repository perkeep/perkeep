#!/usr/bin/perl
#
# Lame upload script for development testing only.  Doesn't do the
# stat step and hard-codes the Go server's upload path (not
# conformant to spec).

use strict;
my $file = shift or die
    "Usage: upload-file.pl: <file>";
-r $file or die "$file isn't readable.";
-f $file or die "$file isn't a file.";

die "bogus filename" if $file =~ /[ <>&\!]/;

my $sha1 = `sha1sum $file`;
chomp $sha1;
$sha1 =~ s/\s.+//;

system("curl", "-u", "foo:foo", "-F", "sha1-$sha1=\@$file",
       "http://127.0.0.1:3179/bs/camli/upload") and die "upload failed.";
print "Uploaded http://127.0.0.1:3179/bs/camli/sha1-$sha1\n";
