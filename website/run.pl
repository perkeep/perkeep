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
push @args, "go", "run", "camweb.go", "logging.go", "godoc.go", "format.go", "dirtrees.go";
push @args, "--root=$Bin";
push @args, "--logdir=$logdir";
push @args, "--buildbot_host=build.camlistore.org";
push @args, "--buildbot_backend=http://c1.danga.com:8080";
if ($in_prod) {
    push @args, "--http=:8080";
    push @args, "--https=:4430";
    push @args, "--gerrithost=ec2-107-22-182-135.compute-1.amazonaws.com";
    push @args, "--tlscert=$HOME/etc/ssl.crt";
    push @args, "--tlskey=$HOME/etc/ssl.key";
    while (1) {
        system(@args);
        print STDERR "Exit: $?; sleeping/restarting...\n";
        sleep 5;
    }
} else {
    push @args, "--http=127.0.0.1:8080"; # localhost avoids Mac firewall warning
    exec(@args);
    die "Failed to exec: $!";
}
