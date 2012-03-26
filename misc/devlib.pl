use strict;

use FindBin qw($Bin);

sub build_bin {
    my $target = shift;

    $ENV{GOBIN} = find_gobin();
    system("go", "install", $target) and die "go install $target failed";
    $target =~ s!.+/!!;
    my $bin = "$ENV{GOBIN}/$target";

    # TODO(bradfitz): workaround for now, until GOBIN bug is fixed.
    unless (-x $bin) {
	my $badbin = $ENV{GOPATH};
	$badbin =~ s/:.*//;
	$badbin .= "/bin";
	die "no GOBIN?" unless $badbin && -d $badbin;
	$bin = "$badbin/$target";
	system("chmod", "+x", $bin) unless -x $bin;
	return $bin;
    }

    system("chmod", "+x", $bin) unless -x $bin;
    return $bin;
}

sub find_bin {
    my $target = shift;
    $target =~ s!.+/!!;
    my $gp = find_arch_gopath();
    return "$gp/bin/$target";
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
