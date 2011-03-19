#!/usr/bin/perl
#
# Common test library for camsigd (sigserver)

package CamsigdTest;

use strict;
use Test::More;
use FindBin;
use LWP::UserAgent;
use HTTP::Request;
use Fcntl;

our $BINARY = "$FindBin::Bin/../sigserver";

sub start {
    my ($port_rd, $port_wr, $exit_rd, $exit_wr);
    my $flags;
    pipe $port_rd, $port_wr;
    pipe $exit_rd, $exit_wr;

    $flags = fcntl($port_wr, F_GETFD, 0);
    fcntl($port_wr, F_SETFD, $flags & ~FD_CLOEXEC);
    $flags = fcntl($exit_rd, F_GETFD, 0);
    fcntl($exit_rd, F_SETFD, $flags & ~FD_CLOEXEC);

    $ENV{TESTING_PORT_WRITE_FD} = fileno($port_wr);
    $ENV{TESTING_CONTROL_READ_FD} = fileno($exit_rd);
    $ENV{CAMLI_PASSWORD} = "test";

    die "Binary $BINARY doesn't exist\n" unless -x $BINARY;

    my $pid = fork;
    die "Failed to fork" unless defined($pid);
    if ($pid == 0) {
        # child
        exec $BINARY, "-listen=:0";
        die "failed to exec: $!\n";
    }
    close($exit_rd);  # child owns this side
    close($port_wr);  # child owns this side

    print "Waiting for server to start...\n";
    my $line = <$port_rd>;
    close($port_rd);

    # Parse the port line out
    chomp $line;
    # print "Got port line: $line\n";
    die "Failed to start, no port info." unless $line =~ /:(\d+)$/;
    my $port = $1;

    return CamsigdTest::Server->new($pid, $port, $exit_wr);
}

package CamsigdTest::Server;

sub new {
    my ($class, $pid, $port, $pipe_writer) = @_;
    return bless {
        pid => $pid,
        port => $port,
        pipe_writer => $pipe_writer,
    };
}

sub DESTROY {
    my $self = shift;
    my $pipe = $self->{pipe_writer};
    syswrite($pipe, "EXIT\n", 5);
}

sub root {
    my $self = shift;
    return "http://localhost:$self->{port}";
}

1;
