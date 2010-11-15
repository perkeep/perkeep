#!/usr/bin/perl
#
# Common test library for camsigd

package CamsigdTest;

use strict;
use Test::More;
use FindBin;
use IPC::Open3;

our $BINARY = "$FindBin::Bin/../run.sh";

sub start {
    my $pid = 1;
}

package CamsigdTest::Server;

sub new {
    my ($class, $pid) = @_;
    return bless { pid => $pid };
}

1;
