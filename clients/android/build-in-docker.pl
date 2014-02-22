#!/usr/bin/perl

use strict;
use File::Path qw(make_path);

die "This script is meant to be run within the camlistore/android Docker contain. Run 'make env' to build it.\n"
    unless $ENV{IN_DOCKER};

my $mode = shift || "debug";

my $ANDROID = "/src/camlistore.org/clients/android";
my $ASSETS = "$ANDROID/assets";
my $GENDIR = "$ANDROID/gen/org/camlistore";

umask 0;
make_path($GENDIR, { mode => 0755 }) unless -d $GENDIR;

$ENV{GOROOT} = "/usr/local/go";
$ENV{GOBIN} = $GENDIR;
$ENV{GOPATH} = "/";
$ENV{GOARCH} = "arm";
print "Building ARM camlistore.org/cmd/camput\n";
system("/usr/local/go/bin/go", "install", "camlistore.org/cmd/camput")
    and die "Failed to build camput";

system("cp", "-p", "$GENDIR/linux_arm/camput", "$ASSETS/camput.arm")
    and die "cp failure";
# TODO: build an x86 version too? if/when those Android devices matter.

{
    open(my $vfh, ">$ASSETS/camput-version.txt") or die "open camput-version error: $!";
    # TODO(bradfitz): make these values automatic, and don't make the
    # "Version" menu say "camput version" when it runs. Also maybe
    # keep a history of these somewhere more convenient.
    print $vfh "app 0.6.1 camput ccacf764 go 70499e5fbe5b";
}

chdir $ASSETS or  die "can't cd to assets dir";

my $digest = `openssl sha1 camput.arm`;
chomp $digest;
print "ARM camput is $digest\n";
die "No digest" unless $digest;
write_file("$GENDIR/ChildProcessConfig.java", "package org.camlistore; public final class ChildProcessConfig { // $digest\n}");

print "Running ant $mode\n";
chdir $ANDROID or die "can't cd to android dir";
exec "ant",
    "-Dsdk.dir=/usr/local/android-sdk-linux",
    "-Dkey.store=/keys/android-camlistore.keystore",
    "-Dkey.alias=camkey",
    $mode;

sub write_file {
    my ($file, $contents) = @_;
    if (open(my $fh, $file)) {
        my $cur = do { local $/; <$fh> };
        return if $cur eq $contents;
    }
    open(my $fh, ">$file") or die "Failed to open $file: $!";
    print $fh $contents;
    close($fh) or die "Close: $!";
    print "Wrote $file\n";
}
