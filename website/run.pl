#!/usr/bin/perl

use strict;
use FindBin qw($Bin);

my $logdir = "$Bin/../logs";

unless (-d $logdir) {
    mkdir $logdir, 0700 or die "mkdir: $!";
}

my $HOME = $ENV{HOME};
chdir $Bin or die;

print STDERR "Running camweb in $Bin on port 8080\n";

my $in_prod = -e "$HOME/etc/ssl.key"; # heuristic. good enough.

my @args;
push @args, "go", "run", "camweb.go", "logging.go", "contributors.go", "godoc.go", "format.go", "dirtrees.go", "email.go";
push @args, "--root=$Bin";
push @args, "--logdir=$logdir";
push @args, "--buildbot_host=build.camlistore.org";
push @args, "--buildbot_backend=http://c1.danga.com:8080";
push @args, "--also_run=$Bin/scripts/run-blobserver";
if ($in_prod) {
    push @args, "--email_dest=camlistore-commits\@googlegroups.com";
    push @args, "--http=:8080";
    push @args, "--https=:4430";
    push @args, "--tlscert=$HOME/etc/ssl.crt";
    push @args, "--tlskey=$HOME/etc/ssl.key";
    while (1) {
        system(@args);
        print STDERR "Exit: $?; sleeping/restarting...\n";
        sleep 5;
    }
} else {
    my $pass_file = "$ENV{HOME}/.config/camlistore/camorg-blobserver.pass";
    unless (-s $pass_file) {
        `mkdir -p $ENV{HOME}/.config/camlistore/`;
        open (my $fh, ">$pass_file");
        print $fh "foo\n";
        close($fh);
    }
    # These https certificate and key are the default ones used by devcam server.
    die "TLS cert or key not initialized; run devcam server --tls" unless -e "$Bin/../config/tls.crt" && -e "$Bin/../config/tls.key";
    push @args, "--tlscert=$Bin/../config/tls.crt";
    push @args, "--tlskey=$Bin/../config/tls.key";
    push @args, "--http=127.0.0.1:8080";
    push @args, "--https=127.0.0.1:4430";
    push @args, @ARGV;
    exec(@args);
    die "Failed to exec: $!";
}
