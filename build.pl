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
my $opt_test;
my $opt_deps;

chdir($FindBin::Bin) or die "Couldn't chdir to $FindBin::Bin: $!";

GetOptions("list" => \$opt_list,
           "eachclean" => \$opt_eachclean,
           "verbose" => \$opt_verbose,
           "test" => \$opt_test,
           "deps" => \$opt_deps,
    ) or usage();

sub usage {
    die <<EOM
Usage:
  build.pl all         # build all
  build.pl allfast     # build all targets that compile quickly
  build.pl clean       # clean all
  build.pl REGEXP      # builds specific target, if it matches any targets
  build.pl --list
  build.pl --eachclean # builds each target with a full clean before it
                       # (for testing that dependencies are correct)
  build.pl --deps      # Show each target's dependencies

Other options:
  --verbose|-v         Verbose
  --test|-t            Run tests where found
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

if ($opt_deps)  {
    foreach my $target (sort keys %targets) {
        find_go_camli_deps($target);
        my $t = $targets{$target} or die;
        print "$target: @{ $t->{deps} }\n";
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

if ($target eq "allfast") {
    foreach my $target (sort grep { !$targets{$_}{tags}{not_in_all} } keys %targets) {
        build($target);
    }
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
        for my $pkg ("camli") {
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

# Returns a help message on a build failure of a given target.
sub fixit_tip {
    my $target = shift;
    if ($target =~ /\bgo\b/) {
        my $gover = `gotry runtime 'Version()'`;
        unless ($gover =~ /Version.+"(.+)"/) {
            return "Failed to find 'gotry'.  Is Go installed?  Or have you put \$GOROOT/bin in your \$PATH?";
        }
        $gover = $1;
        if ($gover =~ /release/) {
            return "You're running a release version of Go ($gover) but \n".
                "Camlistore generally tracks the 'weekly' releases.\n".
                "See: http://blog.golang.org/2011/03/go-becomes-more-stable.html\n";
        }
        unless ($gover =~ /weekly\.(\d\d\d\d)-(\d\d)-(\d\d)/) {
            return "Failed to parse your Go version.  You have \"$gover\" but since\n".
                "I can't parse it, I can't tell you if it's too old or not.\n";
        }
        my ($yyyy, $mm, $dd) = ($1, $2, $3);
        # TODO: check the internet to see what the latest Go weekly is?
        # Or keep it in git here?  Or go purely on number of days passed?
        return "You're running Go weekly release $gover; maybe it's too old?";
    }

    if ($target =~ /\bandroid\b/) {
        return "Have you installed the Android SDK, installed ant, set \$JAVA_HOME?\n".
            "Unset \$JAVA_HOME if it points to a bogus place? Run update-java-alternatives?\n".
            "See errors above.";
    }

    return "";
}

my $did_go_check = 0;
sub perform_go_check() {
    return if $did_go_check++;
    if ($ENV{GOROOT}) {
        die "Your \$GOROOT environment variable isn't a directory.\n" unless -d $ENV{GOROOT};
        return 1;
    }
    # No GOROOT set; see if they have 8g or 6g
    if (`which 8g` =~ /\S/ || `which 6g` =~ /\S/) {
        die "You seem to have Go installed, but you don't have your ".
            "\$GOROOT environment variable set.\n".
            "Can't build without it.\n";
    }
    die "You don't seem to have Go installed.  See:\n\n   http://golang.org/doc/install.html\n\n";
}

sub build {
    my @history = @_;
    my $target = $history[0];

    if ($target =~ m!/go/!) {
        perform_go_check();
    }

    my $already_built = $built{$target} || 0;
    v("Building '$target' (already_built=$already_built; via @history)");
    return if $already_built;
    $built{$target} = 1;

    if ($target =~ /^ext:(.+)/) {
        die "\$GOROOT not set" unless $ENV{GOROOT} && -d $ENV{GOROOT};
        my $golib = $1;
        my @files = glob("$ENV{GOROOT}/pkg/*/${golib}.a");
        unless (@files) {
            die "You need to run:\n\n\tgoinstall $golib\n";
        }
        return;
    }
    
    my $t = $targets{$target} or die "Bogus or undeclared build target: $target\n";

    # Add auto-learned dependencies.
    find_go_camli_deps($target);
    gen_target_makefile($target);

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
        my $help_msg = fixit_tip($target);
        if ($help_msg) {
            $help_msg = "\nPossible tip: $help_msg\n\n";
        }
        my $deps;
        if ($chain) {
            $deps = " (via deps: $chain)";
        }
        die "\nError building $target$deps\n$help_msg";
    }
    v("Built '$target'");

    if ($opt_test && !$t->{tags}{skip_tests}) {
        opendir(my $dh, $target) or die;
        my @test_files = grep { /_test\.go$/ } readdir($dh);
        closedir($dh);
        if (@test_files) {
            if (system("make", @quiet, "-C", $target, "test") != 0) {
                die "Tests failed for $target\n";
            }
        }
    }
}

sub find_go_camli_deps {
    my $target = shift;
    if ($target =~ /\bthird_party\b/) {
        # Skip third-party stuff.
        return;
    }
    unless ($target =~ m!lib/go/camli! ||
            $target =~ m!(server|clients)/go\b!) {
        return;
    }
    my $t = $targets{$target} or die "Bogus or undeclared build target: $target\n";

    opendir(my $dh, $target) or die;
    my @go_files = grep { !m!^\.\#! } grep { !/_testmain\.go$/ } grep { /\.go$/ } readdir($dh);
    closedir($dh);

    # TODO: just stat the files first and keep a cache file of the
    # deps somewhere (the header of the generated Makefile?)  but
    # maybe it's not worth it.  for now we'll parse all the files
    # every time to find their deps and also generate the Makefile
    # every time.
    
    my @deps;
    my %seen;  # $dep -> 1
    for my $f (@go_files) {
        open(my $fh, "$target/$f") or die "Failed to open $target/$f: $!";
        my $src = do { local $/; <$fh>; };
        unless ($src =~ m!\bimport\s*\((.+?)\)!s) {
            die "Failed to parse imports from $target/$f.\n".
                "No imports(...) block?  Um, add a fake one.  :)\n";
        }
        my $imports = $1;
        while ($imports =~ m!"(camli\b.+?)"!g) {
            my $dep = "lib/go/$1";
            push @deps, $dep unless $seen{$dep}++;
        }
    }

    foreach my $dep (@deps) {
        unless (grep { $_ eq $dep } @{$t->{deps}}) {
            push @{$t->{deps}}, $dep;
        }
    }
}

sub gen_target_makefile {
    my $target = shift;
    my $type = "";
    if ($target =~ m!lib/go/camli!) {
        $type = "pkg";
    } elsif ($target =~ m!(server|clients)/go\b!) {
        $type = "cmd";
    } else {
        return;
    }
    my $t = $targets{$target} or die "Bogus or undeclared build target: $target\n";
    
    my @deps = @{$t->{deps}};

    opendir(my $dh, $target) or die;
    my @go_files = grep { !m!^\.\#! } grep { !/_testmain\.go$/ } grep { /\.go$/ } readdir($dh);
    closedir($dh);

    open(my $mf, ">$target/Makefile") or die;
    print $mf "\n\n";
    print $mf "###### NOTE: THIS IS AUTO-GENERATED FROM build.pl IN THE ROOT; DON'T EDIT\n";
    print $mf "\n\n";
    print $mf "include \$(GOROOT)/src/Make.inc\n";
    if (@deps) {
        my $pr = "";
        foreach my $dep (@deps) {
            my $cam_lib = $dep;
            $cam_lib =~ s!^lib/go/!!;
            $pr .= '$(QUOTED_GOROOT)/pkg/$(GOOS)_$(GOARCH)/' . $cam_lib . ".a\\\n\t";
        }
        chop $pr; chop $pr; chop $pr;
        print $mf "PREREQ=$pr\n";
    }
    if ($type eq "pkg") {
        my $targ = $target;
        $targ =~ s!^lib/go/!!;
        print $mf "TARG=$targ\n";
    } else {
        my $targ = $target;
        $targ =~ s!^.*/!!;
        print $mf "TARG=$targ\n";
    }
    my @non_test_files = grep { !/_test\.go/ } @go_files;
    print $mf "GOFILES=@non_test_files\n";
    print $mf "include \$(GOROOT)/src/Make.$type\n";
    close($mf);

    # print "DEPS of $target: @{ $t->{deps} }\n";
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
        s/\#.*//;
        if (m!^\s+\-\s(\S+)\s*$!) {
            my $dep = $1;
            my $t = $targets{$last} or die "Unexpected dependency line: $_";
            push @{$t->{deps}}, $dep;
            next;
        }
        if (m!^\s+\=\s*(\S+)\s*$!) {
            my $tag = $1;
            my $t = $targets{$last} or die "Unexpected dependency line: $_";
            $t->{tags}{$tag} = 1;
            next;
        }
    }
    #use Data::Dumper;
    #print Dumper(\%targets);
}

__DATA__

TARGET: clients/go/camget
TARGET: clients/go/camput
TARGET: clients/go/cammount
TARGET: clients/go/camsync
TARGET: lib/go/camli/auth
TARGET: lib/go/camli/blobref
TARGET: lib/go/camli/blobserver
TARGET: lib/go/camli/blobserver/handlers
TARGET: lib/go/camli/blobserver/localdisk
TARGET: lib/go/camli/blobserver/s3
TARGET: lib/go/camli/client
TARGET: lib/go/camli/httputil
TARGET: lib/go/camli/jsonsign
TARGET: lib/go/camli/lru
TARGET: lib/go/camli/magic
TARGET: lib/go/camli/misc/httprange
TARGET: lib/go/camli/misc/amazon/s3
TARGET: lib/go/camli/mysqlindexer
TARGET: lib/go/camli/schema
TARGET: lib/go/camli/search
TARGET: lib/go/camli/test
TARGET: lib/go/camli/test/asserts
TARGET: lib/go/camli/third_party/github.com/hanwen/go-fuse/fuse
    =skip_tests
TARGET: lib/go/camli/third_party/github.com/Philio/GoMySQL
    =skip_tests
TARGET: lib/go/camli/webserver
TARGET: server/go/blobserver
TARGET: server/go/sigserver
TARGET: website
TARGET: clients/android
    =not_in_all  # too slow
