#!/usr/bin/perl

my $file = shift;
die "$file doesn't exist" unless -e $file;

open(my $fh, $file) or die "failed: $!\n";
my $c = do { local $/; <$fh> };
close($fh);

my $changes = 0;

$changes = 1 if $c =~ s!^(\s+)\"camli/(.+)\"!$1\"camlistore.org/pkg/$2\"!mg;
$changes = 1 if $c =~ s!^(\s+)\"camlistore/(.+)\"!$1\"camlistore.org/$2\"!mg;
$changes = 1 if $c =~ s!^(\s+_ )\"camlistore/(.+)\"!$1\"camlistore.org/$2\"!mg;
$changes = 1 if $c =~ s!/pkg/pkg/!/pkg/!g;
$changes = 1 if $c =~ s!camlistore.org/pkg/third_party/!camlistore.org/third_party/!g;

exit 0 unless $changes;

open(my $fh, ">$file") or die;
print $fh $c;
close($fh);
print STDERR "rewrote $file\n";
