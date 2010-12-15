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

ok($impl->start, "Server started");

$impl->verify_no_blobs;  # also tests some of enumerate

# preupload a blob,
# put a blob,
# get a blob, check headers, content.
# upload a malicious blob (doesn't match sha1), verify it's rejected.
# test enumerate boundaries
# ....
# test auth works on bogus password?  (auth still undefined)

done_testing();

sub usage {
    die "Usage: bs-test.pl [--user= --password=] --impl={go,appengine}\n";
}

package Impl;
use HTTP::Request::Common;
use LWP::UserAgent;
use JSON::Any;
use Test::More;

sub new {
    my ($class, %args) = @_;
    return bless \%args, $class;
}

sub post {
    my ($self, $path, $form) = @_;
    $path ||= "";
    $form ||= {};
    return POST($self->path($path),
                "Authorization" => "Basic dGVzdDp0ZXN0", # test:test
                Content => $form);
}

sub get {
    my ($self, $path, $form) = @_;
    $path ||= "";
    $form ||= {};
    return GET($self->path($path),
               "Authorization" => "Basic dGVzdDp0ZXN0", # test:test
               %$form);
}

sub ua {
    my $self = shift;
    return ($self->{_ua} ||= LWP::UserAgent->new(agent => "camli/blobserver-tester"));
}

sub get_json {
    my ($self, $req, $msg) = @_;
    my $res = $self->ua->request($req);
    ok(defined($res), "got response for HTTP request '$msg'");
    ok($res->is_success, "successful response for HTTP request '$msg'")
        or diag("Status was: " . $res->status_line);
    my $json = JSON::Any->jsonToObj($res->content);
    is("HASH", ref($json), "JSON parsed for HTTP request '$msg'")
        or BAIL_OUT("expected JSON response");
    return $json;
}

sub verify_no_blobs {
    my $self = shift;
    my $req = $self->get("/camli/enumerate-blobs", {
        "after" => "",
        "limit" => 10,
    });
    my $json = $self->get_json($req, "enumerate empty blobs");
    ok(defined($json->{'blobs'}), "enumerate has a 'blobs' key");
    is("ARRAY", ref($json->{'blobs'}), "enumerate's blobs key is an array");
    is(0, scalar @{$json->{'blobs'}}, "no blobs on server");
}

sub path {
    my $self = shift;
    my $path = shift || "";
    my $root = $self->{root} or die "No 'root' for $self";
    return "$root$path";
}

package Impl::Go;
use base 'Impl';
use FindBin;
use LWP::UserAgent;
use HTTP::Request;
use Fcntl;
use File::Temp ();

sub start {
    my $self = shift;

    $self->{_tmpdir_obj} = File::Temp->newdir();
    my $tmpdir = $self->{_tmpdir_obj}->dirname;

    die "Failed to create temporary directory." unless -d $tmpdir;

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
        my @args = ($binary, "-listen=:0", "-root=$tmpdir");
        print STDERR "# Running: [@args]\n";
        exec @args;
        die "failed to exec: $!\n";
    }
    close($exit_rd);  # child owns this side
    close($port_wr);  # child owns this side

    print "Waiting for Go server to start...\n";
    my $line = <$port_rd>;
    close($port_rd);

    # Parse the port line out
    chomp $line;
    # print "Got port line: $line\n";
    die "Failed to start, no port info." unless $line =~ /:(\d+)$/;
    $self->{port} = $1;
    $self->{root} = "http://localhost:$self->{port}";
    print STDERR "# Running on $self->{root} ...\n";

    # Keep a reference to this to write "EXIT\n" to in order
    # to cleanly shutdown the child camlistored process.
    # If we close it, the child also dies, though.
    $self->{_exit_wr} = $exit_wr;
    return 1;
}

sub DESTROY {
    my $self = shift;
    syswrite($self->{_exit_wr}, "EXIT\n");
}

package Impl::AppEngine;
use base 'Impl';

1;



