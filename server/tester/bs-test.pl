#!/usr/bin/perl
#
# Test script to run against a Camli blobserver to test its compliance
# with the spec.

use strict;
use Getopt::Long;
use LWP;
use Test::More;

my $user;
my $password;
my $implopt; 
GetOptions("user" => \$user,
           "password" => \$password,
           "impl=s" => \$implopt,
    ) or usage();

my $impl;
my %args = (user => $user, password => $password);
if ($implopt eq "go") {
    $impl = Impl::Go->new(%args);
} elsif ($implopt eq "appengine") {
    $impl = Impl::AppEngine->new(%args);
} else {
    die "The --impl flag must be 'go' or 'appengine'.\n";
}

$impl->start;

sub usage {
    die "Usage: bs-test.pl [--user= --password=] --impl={go,appengine}\n";
}

package Impl;

sub new {
    my ($class, %args) = @_;
    return bless \%args, $class;
}

package Impl::Go;
use base 'Impl';
use FindBin;
use LWP::UserAgent;
use HTTP::Request;
use Fcntl;

sub start {
    my $self = shift;

    my $bindir = "$FindBin::Bin/../go/blobserver/";
    my $binary = "$bindir/camlistored";

    chdir($bindir) or die "filed to chdir to $bindir: $!";
    system("make") and die "failed to run make in $bindir";

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

    die "Binary $binary doesn't exist\n" unless -x $binary;

    my $pid = fork;
    die "Failed to fork" unless defined($pid);
    if ($pid == 0) {
        # child
        exec $binary, "-listen=:0";
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
    print "On port: $port\n";
}

package Impl::AppEngine;
use base 'Impl';

1;



