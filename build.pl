#!/usr/bin/perl
#
# Copyright 2011 Google Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#
#

# A simple little build system.
#
# See the configuration at the bottom of this file.

use strict;
use Getopt::Long;
use FindBin;

my $opt_list;
my $opt_eachclean;
my $opt_verbose;

chdir($FindBin::Bin) or die "Couldn't chdir to $FindBin::Bin: $!";

GetOptions("list" => \$opt_list,
           "eachclean" => \$opt_eachclean,
           "verbose" => \$opt_verbose,
    ) or usage();

sub usage {
    die <<EOM
Usage:
  build.pl all         # build all
  build.pl clean       # clean all
  build.pl REGEXP      # builds specific target, if it matches any targets
  build.pl --list
  build.pl --eachclean # builds each target with a full clean before it
                       # (for testing that dependencies are correct)

Other options:
  --verbose|-v         Verbose
EOM
;
}

my %built;  # target -> bool (was it already built?)
my %targets;  # dir -> { deps => [ ... ], } 
read_targets();

if ($opt_list)  {
    print "Available targets:\n";
    foreach my $target (sort keys %targets) {
        print "  * $target\n";
    }
    exit;        
}

if ($opt_eachclean) {
    my $target = shift;
    die "--eachclean doesn't take a target\n" if $target;
    foreach my $target (sort keys %targets) {
        clean();
        %built = ();
        build($target);
    }
    exit;
}

my $target = shift or usage();

if ($target eq "clean") {
    clean();
    exit;
}

if ($target eq "all") {
    foreach my $target (sort keys %targets) {
        build($target);
    }
    exit;
}

my @matches = grep { /$target/ } sort keys %targets;
unless (@matches) {
    die "No build targets patching pattern /$target/\n";
}
if (@matches > 1) {
    if (scalar(grep { $_ eq $target } @matches) == 1) {
        @matches = ($target);
    } else {
        die "Build pattern is ambiguous, matches multiple targets:\n  * " . join("\n  * ", @matches) . "\n";
    }
}

build($matches[0]);

sub v {
    return unless $opt_verbose;
    my $msg = shift;
    chomp $msg;
    print STDERR "# $msg\n";
}

sub clean {
    for my $root ("$ENV{GOROOT}/pkg/linux_amd64",
                 "$ENV{GOROOT}/pkg/linux_386") {
        for my $pkg ("camli", "crypto/openpgp") {
            my $dir = "$root/$pkg";
            next unless -d $dir;
            system("rm", "-rfv", $dir);
        }
    }
    foreach my $target (sort keys %targets) {
        print STDERR "Cleaning $target\n";
        system("make", "-C", $target, "clean");
    }
}

sub build {
    my @history = @_;
    my $target = $history[0];
    my $already_built = $built{$target} || 0;
    v("Building '$target' (already_built=$already_built; via @history)");
    return if $already_built;
    $built{$target} = 1;

    my $t = $targets{$target} or die "Bogus build target: $target\n";

    # Dependencies first.
    my @deps = @{ $t->{deps} };
    if (@deps) {
        v("Deps of '$target' are @deps");
        foreach my $dep (@deps) {
            build($dep, @history);
        }
        v("built deps for $target, now can build");
    }

    my @quiet = ("--silent");
    @quiet = () if $opt_verbose;

    if (system("make", @quiet, "-C", $target, "install") != 0) {
        my $chain = "";
        if (@history > 1) {
            $chain = "(via chain @history)";
        }
        die "Error building $target $chain\n";
    }
    v("Built '$target'");
}

sub read_targets {
    my $last;
    for (<DATA>) {
        if (m!^\TARGET:\s*(.+)\s*$!) {
            my $target = $1;
            $last = $target;
            $targets{$target} ||= { deps => [] };
            next;
        }
        if (m!^\s+\-\s(\S+)\s*$!) {
            my $dep = $1;
            my $t = $targets{$last} or die "Unexpected dependency line: $_";
            push @{$t->{deps}}, $dep;
        }
    }
    #use Data::Dumper;
    #print Dumper(\%targets);
}

__DATA__

TARGET: server/go/httputil
    # (no deps)

TARGET: server/go/auth
    # (no deps)

TARGET: server/go/webserver
    # (no deps)

TARGET: server/go/blobserver
    - server/go/httputil
    - lib/go/blobref
    - lib/go/blobserver
    - lib/go/blobserver/handlers
    - server/go/auth
    - server/go/webserver

TARGET: server/go/sigserver
    - server/go/webserver
    - lib/go/blobref
    - server/go/auth
    - server/go/httputil
    - lib/go/jsonsign
    - lib/go/ext/openpgp/packet
    - lib/go/ext/openpgp/error
    - lib/go/ext/openpgp/armor

TARGET: website
    - lib/go/line

TARGET: clients/go/camput
    - lib/go/client
    - lib/go/blobref
    - lib/go/schema
    - lib/go/jsonsign

TARGET: clients/go/camget
    - lib/go/client
    - lib/go/blobref
    - lib/go/schema

TARGET: lib/go/http
    # (no deps, fork of Go's http library)

TARGET: lib/go/line
    # (no deps, fork of Go's encoding/line library)

TARGET: lib/go/ext/openpgp/error
    # (no deps)

TARGET: lib/go/ext/openpgp/packet
    - lib/go/ext/openpgp/error

TARGET: lib/go/ext/openpgp/armor
    - lib/go/ext/openpgp/error
    - lib/go/ext/openpgp/packet

TARGET: lib/go/schema
    - lib/go/blobref

TARGET: lib/go/client
    - lib/go/http
    - lib/go/blobref

TARGET: lib/go/jsonsign
    - lib/go/blobref
    - lib/go/ext/openpgp/packet
    - lib/go/ext/openpgp/armor
    - lib/go/ext/openpgp/error

TARGET: lib/go/blobref
    # (no deps)

TARGET: lib/go/blobserver
    - lib/go/blobref

TARGET: lib/go/blobserver/handlers
    - server/go/auth
    - server/go/httputil
    - lib/go/blobserver
    - lib/go/httprange

TARGET: lib/go/httprange

