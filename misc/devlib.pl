use strict;

use FindBin qw($Bin);

sub build_bin {
    my $target = shift;
    $ENV{GOBIN} = find_gobin();
    print STDERR "Building $target ...\n";
    system("go", "install", "-v", $target) and die "go install $target failed";
    $target =~ s!.+/!!;
    my $bin = "$ENV{GOBIN}/$target";
    unless (-e $bin) {
        die "Expected binary $bin doesn't exist after installing target $target\n";
    }
    system("chmod", "+x", $bin) unless -x $bin;
    return $bin;
}

sub find_bin {
    my $target = shift;
    $target =~ s!.+/!!;
    my $bin = find_gobin();
    return "$bin/$target";
}

sub find_gobin {
    my $env = `go env`;
    # Note: ignoring cross-compiling environments (GOHOSTOS,
    # GOHOSTARCH) for now at least.
    my ($GOARCH) = $env =~ /^GOARCH=\"(.+)\"/m;
    my ($GOOS) = $env =~ /^GOOS=\"(.+)\"/m;
    die "Failed to find GOARCH and/or GOOS" unless $GOARCH && $GOOS;
    my $bin = "$Bin/gopath/bin/${GOOS}_${GOARCH}";
    mkdir $bin, 0755 unless -d $bin;
    return $bin;
}

1;
