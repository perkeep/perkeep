#!/usr/bin/perl

use strict;
use FindBin qw($Bin);

chdir($Bin) or die;

my $workdir = "$Bin/workdir";
unless (-d $workdir) { 
    mkdir $workdir, 0755 or die;
}

my %proj = (
    "fuse" => {
        git => "https://github.com/hanwen/go-fuse.git",
        copies => [
            # File glob => target directory
            [ "fuse/*.go", "github.com/hanwen/go-fuse/fuse" ]
        ],
    },
    "mysql" => {
        git => "https://github.com/Philio/GoMySQL.git",
        copies => [
            # File glob => target directory
            [ "*.go", "github.com/Philio/GoMySQL" ]
        ],
    },
    "mysqlfork" => {
        git => "https://github.com/camlistore/GoMySQL.git",
        copies => [
            # File glob => target directory
            [ "*.go", "github.com/camlistore/GoMySQL" ]
        ],
    },
    "gomemcache" => {
        git => "https://github.com/bradfitz/gomemcache/",
        copies => [
            # File glob => target directory
            [ "*.go", "github.com/bradfitz/gomemcache" ]
        ],
    },
    "flickr" => {
        git => "https://github.com/mncaudill/go-flickr.git",
        copies => [
            # File glob => target directory
            [ "*", "github.com/mncaudill/go-flickr" ]
        ],
    },
);

foreach my $name (sort keys %proj) {
    next if @ARGV && $name !~ /\Q$ARGV[0]\E/;
    my $p = $proj{$name};

    chdir($workdir) or die;
    $p->{git} or die "no git key defined for $name";
    unless (-d $name) {
        print STDERR "Cloning $name ...\n";
        system("git", "clone", $p->{git}, $name) and die "git clone failure";
    }
    chdir($name) or die;
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



