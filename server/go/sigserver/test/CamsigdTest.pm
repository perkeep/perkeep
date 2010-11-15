#!/usr/bin/perl
#
# Common test library for camsigd

package CamsigdTest;

use strict;
use Test::More;
use FindBin;
use LWP::UserAgent;
use HTTP::Request;
use Fcntl;

our $BINARY = "$FindBin::Bin/../run.sh";
our $pipe_reader;
our $pipe_writer;
BEGIN {
    pipe $pipe_reader, $pipe_writer;
    my $flags = fcntl($pipe_writer, F_GETFD, 0);
    fcntl($pipe_writer, F_SETFD, $flags & ~FD_CLOEXEC);
}

sub start {
    my $up_fd = scalar(fileno($pipe_writer));
    $ENV{TESTING_LISTENER_UP_WRITER_PIPE} = $up_fd;
    my $pid = fork;
    die "Failed to fork" unless defined($pid);
    if ($pid) {
        # child
        exec $BINARY;
        die "failed to exec: $!\n";
    } else {
        print "Waiting for server to start (waiting for byte on fd $up_fd)...\n";
        getc($pipe_reader);
    }
    return CamsigdTest::Server->new($pid);
}

package CamsigdTest::Server;

sub new {
    my ($class, $pid) = @_;
    return bless { pid => $pid };
}

sub DESTROY {
    my $self = shift;
    print "DESTROYING $self; pid=$self->{pid}\n";
    kill 3, $self->{pid};  # SIGQUIT
}

sub root {
    return "http://localhost:2856";
}

1;
