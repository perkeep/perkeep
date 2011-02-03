#!/usr/bin/perl

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
    die "Build pattern is ambiguous, matches multiple targets:\n  * " . join("\n  * ", @matches) . "\n";
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
        if (m!^\./(.+)/Makefile\s*$!) {
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
./server/go/httputil/Makefile
    # (no deps)
./server/go/auth/Makefile
    # (no deps)
./server/go/webserver/Makefile
    # (no deps)
./server/go/blobserver/Makefile
    - server/go/httputil
    - lib/go/blobref
    - lib/go/blobserver
    - server/go/auth
    - server/go/webserver
./server/go/sigserver/Makefile
    - server/go/webserver
    - lib/go/blobref
    - server/go/auth
    - server/go/httputil
    - lib/go/jsonsign
    - lib/go/ext/openpgp/packet
    - lib/go/ext/openpgp/error
    - lib/go/ext/openpgp/armor
./website/Makefile
    - lib/go/line
./clients/go/camput/Makefile
    - lib/go/client
    - lib/go/blobref
    - lib/go/schema
    - lib/go/jsonsign
./clients/go/camget/Makefile
    - lib/go/client
    - lib/go/blobref
    - lib/go/schema
./lib/go/http/Makefile
    # (no deps, fork of Go's http library)
./lib/go/line/Makefile
    # (no deps, fork of Go's encoding/line library)
./lib/go/ext/openpgp/error/Makefile
    # (no deps)
./lib/go/ext/openpgp/packet/Makefile
    - lib/go/ext/openpgp/error
./lib/go/ext/openpgp/armor/Makefile
    - lib/go/ext/openpgp/error
    - lib/go/ext/openpgp/packet
./lib/go/schema/Makefile
    - lib/go/blobref
./lib/go/client/Makefile
    - lib/go/http
    - lib/go/blobref
./lib/go/jsonsign/Makefile
    - lib/go/blobref
    - lib/go/ext/openpgp/packet
    - lib/go/ext/openpgp/armor
    - lib/go/ext/openpgp/error
./lib/go/blobref/Makefile
    # (no deps)
./lib/go/blobserver/Makefile
    - lib/go/blobref
