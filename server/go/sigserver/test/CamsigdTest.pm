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

sub start {
    my $pipe_reader;
    my $pipe_writer;
    pipe $pipe_reader, $pipe_writer;
    my $flags = fcntl($pipe_writer, F_GETFD, 0);
    fcntl($pipe_writer, F_SETFD, $flags & ~FD_CLOEXEC);

    my $up_fd = scalar(fileno($pipe_writer));
    $ENV{TESTING_LISTENER_UP_WRITER_PIPE} = $up_fd;

    my $pid = fork;
    die "Failed to fork" unless defined($pid);
    if ($pid == 0) {
        # child
        exec $BINARY;
        die "failed to exec: $!\n";
    }
    print "Waiting for server to start (waiting for byte on fd $up_fd)...\n";
    getc($pipe_reader);
    close($pipe_reader);
    close($pipe_writer);
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
    my $killed = kill 9, $self->{pid};  # SIGQUIT
    print "Killed = $killed\n";
    waitpid $self->{pid}, 0;
}

sub root {
    return "http://localhost:2856";
}

1;
