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
use Cwd;

my $opt_list;
my $opt_eachclean;
my $opt_verbose;
my $opt_test;
my $opt_deps;
my $opt_updatego;

chdir($FindBin::Bin) or die "Couldn't chdir to $FindBin::Bin: $!";

GetOptions("list" => \$opt_list,
           "eachclean" => \$opt_eachclean,
           "verbose+" => \$opt_verbose,
           "test" => \$opt_test,
           "deps" => \$opt_deps,
           "updatego" => \$opt_updatego,
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
  --updatego           Attempt to update GOROOT to maintainer\'s go version
EOM
;
}

my %built;  # target -> bool (was it already built?)

# Note: the target key is usually the directory, but may be overridden.  use
# the dir($target) function to find the directory
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
        if (@{$t->{testdeps} || []}) {
            print "$target.test: @{ $t->{testdeps} }\n";
        }
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
        test($target);
    }
    exit;
}

if ($opt_updatego) {
  update_go();
}

my $target = shift or usage();

if ($target eq "clean") {
    clean();
    exit;
}

if ($target eq "allfast") {
    my @targets = sort grep { !$targets{$_}{tags}{not_in_all} } keys %targets;
    @targets = filter_os_targets(@targets);
    foreach my $target (@targets) {
        find_go_camli_deps($target);
        build($target);
        test($target);
    }
    record_go_version();
    exit;
}

if ($target eq "all") {
    my @targets = filter_os_targets(sort keys %targets);
    foreach my $target (@targets) {
        build($target);
        test($target);
    }
    record_go_version();
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
test($matches[0]);

sub v {
    return unless $opt_verbose;
    my $msg = shift;
    chomp $msg;
    print STDERR "# $msg\n";
}

sub v2 {
    return unless $opt_verbose >= 2;
    my $msg = shift;
    chomp $msg;
    print STDERR "# $msg\n";
}

sub clean {
    for my $root ("$ENV{GOROOT}/pkg/linux_amd64",
                  "$ENV{GOROOT}/pkg/linux_386") {
        for my $pkg ("camli", "camlistore.org") {
            my $dir = "$root/$pkg";
            next unless -d $dir;
            system("rm", "-rfv", $dir);
        }
    }
    foreach my $target (sort keys %targets) {
        print STDERR "Cleaning $target\n";
        system("make", "-C", dir($target), "clean");
    }
}

# Updates go to maintainer version.
sub update_go {
  # Get revision number:
  my $last = do { open(my $fh, ".last_go_version") or die; local $/; <$fh> };
  $last =~ s!^[68]g version weekly.[0-9]{4}-[0-9]{2}-[0-9]{2} !!;
  chomp $last;
  unless ($last =~ /^(\d+)\+?\s*$/) {
    print "Failed to obtain maintainer's go revision\n";
    return;
  }
  my $maintainer_version = $1;

  print "Updating go to revision: $last\n";
  my $prev_cwd = getcwd;
  if ($ENV{'GOROOT'} eq '') {
    print "\$GOROOT not set!\n";
    return;
  }

  chdir $ENV{'GOROOT'} or die "Chdir to $ENV{'GOROOT'} failed\n";
  system("hg", "pull") and die "Hg pull failed\n";
  system("hg", "update", $maintainer_version) and die "Hg update failed\n";
  print "Building...\n";
  chdir "$ENV{'GOROOT'}/src" or die "Chdir to $ENV{'GOROOT'}/src failed\n";
  system("./all.bash") and die "Go build failed\n";

  chdir $prev_cwd or die "Chdir back to $prev_cwd failed\n";
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
        my $last = do { open(my $fh, ".last_go_version") or die; local $/; <$fh> };
        $last =~ s!^[68]g version !!;
        chomp $last;
        chomp $gover;
        if ($last eq $gover) {
            return "";
        }
        return "You're running Go version: $gover (maintainer's last version used was $last)\nCamlistore generally tracks Go tip closely.\n(run \"hg pull && hg update tip && cd src && ./all.bash\" in \$GOROOT)";

    }

    if ($target =~ /\bandroid\b/) {
        return "Have you installed the Android SDK, installed ant, set \$JAVA_HOME?\n".
            "Unset \$JAVA_HOME if it points to a bogus place? Run update-java-alternatives?\n".
            "See errors above.";
    }

    return "";
}

my $did_go_check = 0;
my $gc_bin;
sub perform_go_check() {
    return if $did_go_check++;
    unless ($ENV{GOROOT}) {
        if (`which 6g` =~ /\S/ || `which 8g` =~ /\S/) {
            die "You seem to have Go installed, but you don't have your ".
                "\$GOROOT environment variable set.\n".
                "Can't build without it.\n";
        }
        die "You don't seem to have Go installed.  See:\n\n   http://golang.org/doc/install.html\n\n";
    }
    die "Your \$GOROOT environment variable isn't a directory.\n" unless -d $ENV{GOROOT};

    foreach my $gc ("6g", "8g") {
        $gc_bin = `which $gc` or next;
        chomp $gc_bin;
        last;
    }
    die "No 6g or 8g found in your \$PATH.\n" unless -x $gc_bin;
    return 1;
}

sub test {
    my $target = shift;
    my $t = $targets{$target} or die "Bogus or undeclared test target: $target\n";
    return if !$opt_test || $t->{tags}{skip_tests};
    build($target);

    # Make sure testdeps are built too.
    my @testdeps = @{ $t->{testdeps} || [] };
    if (@testdeps) {
        v2("Test deps of '$target' are @testdeps");
        foreach my $dep (@testdeps) {
            build($dep);
        }
        v2("built testdeps for $target, now can run tests");
    }

    opendir(my $dh, dir($target)) or die;
    my @test_files = grep { /_test\.go$/ } readdir($dh);
    closedir($dh);
    if (@test_files) {
        if ($target =~ m!\blib/go\b!) {
            my @quiet = ("--silent");
            @quiet = () if $opt_verbose;
            if (system("make", @quiet, "-C", dir($target), "test") != 0) {
                die "Tests failed for $target\n";
            }
        } else {
            my $testv = $opt_verbose ? "-test.v" : "";
            if (system("cd $target && gotest $testv") != 0) {
                die "gotest failed for $target\n";
            }
        }
    }
}

sub build {
    my @history = @_;
    my $target = $history[0];

    my $is_go = $target =~ m!/go/!;
    if ($is_go) {
        perform_go_check();
    }

    my $already_built = $built{$target} || 0;
    v2("Building '$target' (already_built=$already_built; via @history)");
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
        v2("Deps of '$target' are @deps");
        foreach my $dep (@deps) {
            build($dep, @history);
        }
        v2("built deps for $target, now can build");
    }

    my @quiet = ("--silent");
    @quiet = () if $opt_verbose;

    my $build_command = sub {
        return system("make", @quiet, "-C", dir($target), "install") == 0;
    };

    if (!$build_command->()) {
        my $chain = "";
        if (@history > 1) {
            $chain = "via chain @history";
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
}

sub filter_go_os {
    my @good;
    my $is_windows = $^O eq "msys" || $^O eq "MSWin32";
    foreach my $f (@_) {
        my $for_unix = $f =~ /_unix\.go$/;
        my $for_windows = $f =~ /_windows\.go$/;
        next if $for_unix && $is_windows;
        next if $for_windows && !$is_windows;
        push @good, $f;
    }
    return @good;
}

sub find_go_camli_deps {
    my $target = shift;
    if ($target =~ /\bthird_party\b/) {
        # Skip third-party stuff.
        return;
    }
    unless ($target =~ m!lib/go/camli! ||
            $target =~ m!^camlistore\.org/! ||
            $target =~ m!(server|clients)/go\b!) {
        return;
    }

    my $t = $targets{$target} or die "Bogus or undeclared build target: $target\n";
    my $target_dir = dir($target);
    opendir(my $dh, $target_dir) or die "Failed to open directory: $target\n";
    my @go_files = grep { !m!^\.\#! } grep { !/_testmain\.go$/ } grep { /\.go$/ } readdir($dh);
    @go_files = filter_go_os(@go_files);
    closedir($dh);

    # TODO: just stat the files first and keep a cache file of the
    # deps somewhere (the header of the generated Makefile?)  but
    # maybe it is not worth it.  for now we'll parse all the files
    # every time to find their deps and also generate the Makefile
    # every time.

    my %seendep;     # dep -> 1
    my %seentestdep; # dep -> 1, for _test.go files

    for my $f (@go_files) {
        my $depref = ($f =~ /_test\.go$/) ? \%seentestdep : \%seendep;
        open(my $fh, "$target_dir/$f") or die "Failed to open $target_dir/$f: $!";
        my $src = do { local $/; <$fh>; };
        if ($src =~ m!^import\b!m) {
            unless ($src =~ m!\bimport\s*\((.+?)\)!s) {
                die "Failed to parse imports from $target_dir/$f.\n".
                    "No imports(...) block?  Um, add a fake one.  :)\n";
            }
            my $imports = $1;
            while ($imports =~ m!"(camli\b.+?)"!g) {
                my $dep = "lib/go/$1";
                $depref->{$dep} = 1;
            }
            while ($imports =~ m!"(camlistore\.org/.+?)"!g) {
                my $dep = $1;
                $depref->{$dep} = 1;
            }
        }
    }

    $t->{deps} = [ sort keys %seendep ];
    $t->{testdeps} = [ sort keys %seentestdep ];
    v2("Scanned deps of $target in $target_dir, got: [@{$t->{deps}}] test:[@{$t->{testdeps}}]")
}

sub dir {
    my $target = shift;
    my $t = $targets{$target} or die "Bogus or undeclared build target: $target\n";
    return $t->{tags}{dir} || $target;
}

sub gen_target_makefile {
    my $target = shift;
    my $type = "";
    if ($target =~ m!lib/go/camli!) {
        $type = "pkg";
    } elsif ($target =~ m!(server|clients)/go\b!) {
        $type = "cmd";
    } elsif ($target =~ m!^camlistore\.org/!) {
        # type to be set later
    } else {
        return;
    }
    my $t = $targets{$target} or die "Bogus or undeclared build target: $target\n";
    my $override_target;  # optional override of what to write to Makefile
    my @deps = @{$t->{deps}};

    my $target_dir = dir($target);

    opendir(my $dh, $target_dir) or die;
    my @dir_files = readdir($dh);
    my @go_files = grep { !m!^\.\#! } grep { !/_testmain\.go$/ } grep { /\.go$/ } @dir_files;
    @go_files = filter_go_os(@go_files);
    closedir($dh);

    if ($t->{tags}{fileembed}) {
        $type = "pkg";
        die "No fileembed.go file in $target, but declared with tag 'fileembed'\n" unless
            grep { $_ eq "fileembed.go" } @go_files;
        open(my $fe, "$target_dir/fileembed.go") or die;
        my ($pattern, $embed_pkg);
        while (<$fe>) {
            if (!$embed_pkg && /^package (\S+)/) {
                $embed_pkg = $1;
            }
            if (!$pattern && /^#fileembed pattern (\S+)\s*$/) {
                $pattern = $1;
            }
            if (!$override_target && /^#fileembed target (\S+)\s*$/) {
                $override_target = $1;
            }
        }
        close($fe);
        die "No #filepattern found in $target_dir/fileembed.go" unless $pattern;
        foreach my $resfile (grep { /^$pattern$/o } @dir_files) {
            my $gores = "_embed_${resfile}.go";
            push @go_files, $gores;
            if (modtime("$target_dir/$gores") < max(modtime("build.pl"), modtime("$target_dir/$resfile"))) {
                generate_embed_file($embed_pkg, $resfile, "$target_dir/$resfile", "$target_dir/$gores");
            }
        }
    }

    # Generate the Makefile
    my $mfc = "\n\n";
    $mfc .= "###### NOTE: THIS IS AUTO-GENERATED FROM build.pl IN THE ROOT; DON'T EDIT\n";
    $mfc .= "\n\n";
    $mfc .= "include \$(GOROOT)/src/Make.inc\n";
    my $pr = "";
    if (@deps) {
        foreach my $dep (@deps) {
            my $cam_lib = $dep;
            $cam_lib =~ s!^lib/go/!!;
            $pr .= '$(QUOTED_GOROOT)/pkg/$(GOOS)_$(GOARCH)/' . $cam_lib . ".a\\\n\t";
        }
        chop $pr; chop $pr; chop $pr;
    }
    $mfc .= "PREREQ=$gc_bin $pr\n";
    if ($type eq "pkg") {
        my $targ = $target;
        $targ =~ s!^lib/go/!!;
        $targ = $override_target || $targ;
        $mfc .= "TARG=$targ\n";
    } else {
        my $targ = $target;
        $targ =~ s!^.*/!!;
        $mfc .= "TARG=$targ\n";
    }
    my @non_test_files = grep { !/_test\.go/ } @go_files;
    $mfc .= "GOFILES=@non_test_files\n";
    $mfc .= "include \$(GOROOT)/src/Make.$type\n";

    set_file_contents("$target_dir/Makefile", $mfc);

    # print "DEPS of $target: @{ $t->{deps} }\n";
}

sub set_file_contents {
    my ($fn, $new) = @_;
    if (-e $fn) {
        open(my $fh, $fn) or die "Failed to write to $fn: $!";
        my $cur = do { local $/; <$fh> };
        return if $new eq $cur;
    }
    open(my $fh, ">$fn") or die;
    print $fh $new;
    close($fh) or die;
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
        if (m!^\s+\=\s*(\S+):(\S+)\s*$!) {
            my $tag = $1;
            my $t = $targets{$last} or die "Unexpected dependency line: $_";
            $t->{tags}{$tag} = $2;
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

sub record_go_version {
    return unless $ENV{USER} eq "bradfitz";
    return unless `uname -m` =~ /x86_64/;
    my $ver = `6g -V`;
    open(my $fh, ">.last_go_version") or return;
    print $fh $ver;
    close($fh);
}

sub filter_os_targets {
    my @targets = @_;
    my @out;
    my $is_linux = (`uname` =~ /linux/i);
    foreach my $t (@targets) {
        if ($targets{$t}{tags}{only_os_linux} && !$is_linux) {
            next;
        }
        push @out, $t;
    }
    return @out;
}

sub modtime {
    my $file = shift;
    my @st = stat($file);
    return $st[9];
}

sub max {
    my $n = shift;
    foreach my $c (@_) {
        $n = $c if $c > $n;
    }
    return $n;
}

sub generate_embed_file {
    my ($pkg, $base_file, $source, $dest) = @_;
    print STDERR "# Generating $base_file -> $dest\n";
    open(my $sf, $source) or die "Error opening $source for embedding: $!\n";
    open(my $dest, ">$dest") or die "Error creating $dest for embedding: $!\n";
    my $contents = do { local $/; <$sf> };
    print $dest "// THIS FILE IS AUTO-GENERATED FROM $base_file\n";
    print $dest "// DO NOT EDIT.\n";
    print $dest "package $pkg\n";
    my $escaped;
    for my $i (0..length($contents)-1) {
        my $ch = substr($contents, $i, 1);
        my $b = ord($ch);
        if ($b >= 32 && $b < 127 && $b != ord("\"") && $b != ord("\\")) {
            $escaped .= $ch;
        } else {
            $escaped .= "\\x" . sprintf("%02x", $b);
        }
        if (++$i % 70 == 50) {
            $escaped .= "\"+\n\t\"";
        }
    }
    print $dest "func init() {\n\tFiles.Add(\"$base_file\", \"$escaped\");\n}\n";
}

__DATA__

TARGET: clients/go/camdbinit
TARGET: clients/go/camget
TARGET: clients/go/camgsinit
TARGET: clients/go/camput
TARGET: clients/go/cammount
    =only_os_linux
TARGET: clients/go/camsync
TARGET: clients/go/camwebdav
TARGET: lib/go/camli/auth
TARGET: lib/go/camli/blobref
TARGET: lib/go/camli/blobserver
TARGET: lib/go/camli/blobserver/cond
TARGET: lib/go/camli/blobserver/google
TARGET: lib/go/camli/blobserver/handlers
TARGET: lib/go/camli/blobserver/localdisk
TARGET: lib/go/camli/blobserver/remote
TARGET: lib/go/camli/blobserver/replica
TARGET: lib/go/camli/blobserver/shard
TARGET: lib/go/camli/blobserver/s3
TARGET: lib/go/camli/cacher
TARGET: lib/go/camli/client
TARGET: lib/go/camli/db
TARGET: lib/go/camli/db/dbimpl
TARGET: lib/go/camli/errorutil
TARGET: lib/go/camli/fs
TARGET: lib/go/camli/googlestorage
    =skip_tests
TARGET: lib/go/camli/httputil
TARGET: lib/go/camli/jsonconfig
TARGET: lib/go/camli/jsonsign
TARGET: lib/go/camli/lru
TARGET: lib/go/camli/magic
TARGET: lib/go/camli/misc
TARGET: lib/go/camli/misc/amazon/s3
TARGET: lib/go/camli/misc/fileembed
TARGET: lib/go/camli/misc/httprange
TARGET: lib/go/camli/misc/gpgagent
TARGET: lib/go/camli/misc/pinentry
TARGET: lib/go/camli/misc/resize
TARGET: lib/go/camli/mysqlindexer
TARGET: lib/go/camli/netutil
TARGET: lib/go/camli/osutil
TARGET: lib/go/camli/rollsum
TARGET: lib/go/camli/schema
TARGET: lib/go/camli/search
TARGET: lib/go/camli/test
TARGET: lib/go/camli/test/asserts
TARGET: lib/go/camli/third_party/code.google.com/goauth2/oauth
TARGET: lib/go/camli/third_party/github.com/bradfitz/gomemcache
TARGET: lib/go/camli/third_party/github.com/hanwen/go-fuse/fuse
    =skip_tests
    =only_os_linux
TARGET: lib/go/camli/third_party/github.com/mncaudill/go-flickr/
    =skip_tests
TARGET: lib/go/camli/third_party/github.com/Philio/GoMySQL
    =skip_tests
TARGET: lib/go/camli/third_party/github.com/camlistore/GoMySQL
    =skip_tests
TARGET: lib/go/camli/webserver
TARGET: server/go/camlistored
TARGET: camlistore.org/server/uistatic
    =fileembed
    =dir:server/go/camlistored/ui
TARGET: server/go/sigserver
TARGET: website
TARGET: clients/android
    =not_in_all  # too slow
# foo
