use strict;
use Time::HiRes ();
use FindBin qw($Bin);

sub build_bin {
    my $target = shift;
    my $final_bin = find_bin($target);
    if ($ENV{CAMLI_FAST_DEV}) {
        return $final_bin;
    }

    my $full_target = $target;
    $full_target =~ s!^\./((cmd|server)/(\w+))$!camlistore.org/$1! or die "Bogus target $target";

    my $mtime = 0;
    if (-f $final_bin) {
        $mtime = (stat($final_bin))[9];
    }

    print STDERR "Building $full_target ...\n";
    my $t0 = Time::HiRes::time();
    system("go", "run", "make.go",
           "--quiet",
           "--embed_static=false",
           "--sqlite=false",
           "--if_mods_since=$mtime",
           "--targets=$full_target")
        and die "go install $target failed";
    my $td = Time::HiRes::time() - $t0;

    print STDERR "Build/init took " . sprintf("%0.03f", $td) . " seconds.\n";
    
    return $final_bin;
}

sub find_bin {
    my $target = shift;
    $target =~ s!.+/!!;
    my $bin = find_gobin();
    return "$bin/$target";
}

sub find_gobin {
    return "$Bin/bin";
}

1;
