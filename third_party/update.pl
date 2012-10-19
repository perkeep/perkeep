#!/usr/bin/perl

use strict;
use FindBin qw($Bin);

die "TODO(bradfitz): update.";

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
    "mysqlfork" => {
        git => "https://github.com/camlistore/GoMySQL.git",
        copies => [
            # File glob => target directory
            [ "*.go", "github.com/camlistore/GoMySQL" ]
        ],
    },
    "goauth" => {
        hg => "https://code.google.com/p/goauth2/",
        copies => [
            # File glob => target directory
            [ "oauth/*.go", "code.google.com/goauth2/oauth" ]
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

# Copies the file globs specified by a project 'copies' value.
sub copy_files {
  my $name = shift;
  my $p = $proj{$name};
  for my $cp (@{$p->{copies}}) {
      my $glob = $cp->[0] or die;
      my $target_dir = $cp->[1] or die;
      system("mkdir", "-p", "$Bin/$target_dir") and die "Failed to make $Bin/$target_dir";
      my @files = glob($glob) or die "Glob '$glob' didn't match any files for project '$name'";
      system("cp", "-p", @files, "$Bin/$target_dir") and die "Copy failed.";
  }
}

# Fetches most recent project sources from git
sub update_git {
  my $name = shift;
  my $p = $proj{$name};

  chdir($workdir) or die;
  unless (-d $name) {
    print STDERR "Cloning $name ...\n";
    system("git", "clone", $p->{git}, $name) and die "git clone failure";
  }

  chdir($name) or die;
  print STDERR "Updating $name ...\n";
  system("git", "pull");
  copy_files($name);
}

# Fetches most recent project sources from mercurial
sub update_hg {
  my $name = shift;
  my $p = $proj{$name};

  chdir($workdir) or die;
  unless (-d $name) {
    print STDERR "Cloning $name ...\n";
    system("hg", "clone", $p->{hg}, $name) and die "hg clone failure";
  }

  chdir($name) or die;
  print STDERR "Updating $name ...\n";
  system("hg", "pull");
  system("hg", "update");
  copy_files($name);
}

foreach my $name (sort keys %proj) {
    next if @ARGV && $name !~ /\Q$ARGV[0]\E/;
    my $p = $proj{$name};

    if ($p->{git}) {
      update_git($name);
    } elsif ($p->{hg}) {
      update_hg($name);
    } else {
      die "No known VCS defined for $name";
    }
}

