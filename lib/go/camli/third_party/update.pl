#!/usr/bin/perl

use strict;
use FindBin qw($Bin);

chdir($Bin) or die;

my $workdir = "$Bin/workdir";
unless (-d $workdir) { 
    mkdir $workdir, 0755 or die;
}

my @proj = (
    {
        name => "fuse",
        git => "https://github.com/hanwen/go-fuse.git",
        worksubdir => "go-fuse",
        copies => [
            # File glob => target directory
            [ "fuse/*.go", "github.com/hanwen/go-fuse/fuse" ]
        ],
    },
    {
        name => "mysql",
        git => "https://github.com/Philio/GoMySQL.git",
        worksubdir => "go-mysql",
        copies => [
            # File glob => target directory
            [ "*.go", "github.com/Philio/GoMySQL" ]
        ],
    },
    {
        name => "mysqlfork",
        git => "https://github.com/camlistore/GoMySQL.git",
        worksubdir => "camli-mysql",
        copies => [
            # File glob => target directory
            [ "*.go", "github.com/camlistore/GoMySQL" ]
        ],
    },
    {
        name => "gomemcache",
        git => "https://github.com/bradfitz/gomemcache/",
        worksubdir => "gomemcache",
        copies => [
            # File glob => target directory
            [ "*.go", "github.com/bradfitz/gomemcache" ]
        ],
    },
);

foreach my $p (@proj) {
    my $name = $p->{name} or die "no name";
    next if @ARGV && $name !~ /\Q$ARGV[0]\E/;

    chdir($workdir) or die;
    $p->{worksubdir} or die "no worksubdir for $name";
    $p->{git} or die "no git for $name";
    unless (-d $p->{worksubdir}) {
        print STDERR "Cloning $name ...\n";
        system("git", "clone", $p->{git}, $p->{worksubdir}) and die "git clone failure";
    }
    chdir($p->{worksubdir}) or die;
    print STDERR "Updating $name ...\n";
    system("git", "pull");
    for my $cp (@{$p->{copies}}) {
        my $glob = $cp->[0] or die;
        my $target_dir = $cp->[1] or die;
        system("mkdir", "-p", "$Bin/$target_dir") and die "Failed to make $Bin/$target_dir";
        my @files = glob($glob) or die "Glob '$glob' didn't match any files for project '$name'";
        system("cp", "-p", @files, "$Bin/$target_dir") and die "Copy failed.";
    }
}



