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

our $BINARY = "$FindBin::Bin/../camsigd";

sub start {
    my $pipe_reader;
    my $pipe_writer;
    pipe $pipe_reader, $pipe_writer;
    my $flags = fcntl($pipe_writer, F_GETFD, 0);
    fcntl($pipe_writer, F_SETFD, $flags & ~FD_CLOEXEC);

    my $up_fd = scalar(fileno($pipe_writer));
    $ENV{TESTING_LISTENER_UP_WRITER_PIPE} = $up_fd;
    $ENV{CAMLI_PASSWORD} = "test";

    die "Binary $BINARY doesn't exist\n" unless -x $BINARY;

    my $pid = fork;
    die "Failed to fork" unless defined($pid);
    if ($pid == 0) {
        # child
        exec $BINARY, "-listen=:0";
        die "failed to exec: $!\n";
    }
    print "Waiting for server to start...\n";
    my $line = <$pipe_reader>;
    close($pipe_reader);
    close($pipe_writer);

    # Parse the port line out
    print "Got port line: $line\n";
    chomp $line;
    die "Failed to start, no port info." unless $line =~ /:(\d+)$/;
    my $port = $1;

    return CamsigdTest::Server->new($pid, $port);
}

package CamsigdTest::Server;

sub new {
    my ($class, $pid, $port) = @_;
    return bless { pid => $pid, port => $port };
}

sub DESTROY {
    my $self = shift;
    print "DESTROYING $self; pid=$self->{pid}\n";
    my $killed = kill 9, $self->{pid};  # SIGQUIT
    print "Killed = $killed\n";
    waitpid $self->{pid}, 0;
}

sub root {
    my $self = shift;
    return "http://localhost:$self->{port}";
}

1;
