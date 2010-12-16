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
$impl->test_preupload_and_upload;

# upload a malicious blob (doesn't match sha1), verify it's rejected.
# test multiple uploads in a batch
# test uploads in serial
# test enumerate boundaries
# interrupt a POST upload in the middle; verify no straggler on disk in subsequent GET
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
use Digest::SHA1 qw(sha1_hex);
use URI::URL ();
use Data::Dumper;

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

sub upload_request {
    my ($self, $upload_url, $blobref_to_blob_map) = @_;
    return POST($upload_url,
                "Content_Type" => 'form-data',
                "Authorization" => "Basic dGVzdDp0ZXN0", # test:test
                Content => [
                    %$blobref_to_blob_map
                ]);
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

sub path {
    my $self = shift;
    my $path = shift || "";
    my $root = $self->{root} or die "No 'root' for $self";
    return "$root$path";
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

sub test_preupload_and_upload {
    my $self = shift;
    my ($req, $res);

    my $blob = "This is a line.\r\nWith mixed newlines\rFoo\nAnd binary\0data.\0\n\r.";
    my $blobref = "sha1-" . sha1_hex($blob);

    # Bogus method.
    $req = $self->get("/camli/preupload", {
        "camliversion" => 1,
        "blob1" => $blobref,
    });
    $res = $self->ua->request($req);
    ok(!$res->is_success, "returns failure for GET on /camli/preupload");

    # Correct method, but missing camliVersion.
    $req = $self->post("/camli/preupload", {
        "blob1" => $blobref,
    });
    $res = $self->ua->request($req);
    ok(!$res->is_success, "returns failure for missing camliVersion param on preupload");

    # Valid pre-upload
    $req = $self->post("/camli/preupload", {
        "camliversion" => 1,
        "blob1" => $blobref,
    });
    my $jres = $self->get_json($req, "valid preupload");
    print STDERR "preupload response: ", Dumper($jres);
    ok($jres, "valid preupload JSON response");
    for my $f (qw(alreadyHave maxUploadSize uploadUrl uploadUrlExpirationSeconds)) {
        ok(defined($jres->{$f}), "required field '$f' present");
    }
    my $already = $jres->{alreadyHave};
    is(ref($already), "ARRAY", "alreadyHave is an array");
    is(scalar(@$already), 0, "server doesn't have this blob yet.");
    like($jres->{uploadUrlExpirationSeconds}, qr/^\d+$/, "uploadUrlExpirationSeconds is numeric");
    my $upload_url = URI::URL->new($jres->{uploadUrl});
    ok($upload_url, "valid uploadUrl");
    # TODO: are relative URLs allowed in uploadUrl?

    # Do the actual upload
    my $upreq = $self->upload_request($upload_url, {
        $blobref => $blob,
    });
    my $upres = $self->get_json($upreq, "upload");
    ok($upres, "Upload was success");
    print STDERR "# upload response: ", Dumper($upres);

    for my $f (qw(uploadUrlExpirationSeconds uploadUrl maxUploadSize received)) {
        ok(defined($upres->{$f}), "required upload response field '$f' present");
    }
    like($upres->{uploadUrlExpirationSeconds}, qr/^\d+$/, "uploadUrlExpirationSeconds is numeric");
    is(ref($upres->{received}), "ARRAY", "'received' is an array")
        or BAIL_OUT();
    my $got = $upres->{received};
    is(scalar(@$got), 1, "got one file");
    is($got->[0]{blobRef}, $blobref, "received[0] 'blobRef' matches");
    is($got->[0]{size}, length($blob), "received[0] 'size' matches");
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
use IO::Socket::INET;

sub start {
    my $self = shift;

    my $dev_appserver = `which dev_appserver.py`;
    chomp $dev_appserver;
    unless ($dev_appserver && -x $dev_appserver) {
        $dev_appserver = "$ENV{HOME}/sdk/google_appengine/dev_appserver.py";
        unless (-x $dev_appserver) {
            die "No dev_appserver.py in \$PATH nor in \$HOME/sdk/google_appengine/dev_appserver.py\n";
        }
    }

    $self->{_tempdir_blobstore_obj} = File::Temp->newdir();
    $self->{_tempdir_datastore_obj} = File::Temp->newdir();
    my $datadir = $self->{_tempdir_blobstore_obj}->dirname;
    my $blobdir = $self->{_tempdir_datastore_obj}->dirname;

    my $port;
    while (1) {
        $port = int(rand(30000) + 1024);
        my $sock = IO::Socket::INET->new(Listen    => 5,
                                         LocalAddr => '127.0.0.1',
                                         LocalPort => $port,
                                         ReuseAddr => 1,
                                         Proto     => 'tcp');
        if ($sock) {
            last;
        }
    }
    $self->{port} = $port;
    $self->{root} = "http://localhost:$self->{port}";

    my $pid = fork;
    die "Failed to fork" unless defined($pid);
    if ($pid == 0) {
        my $appdir = "$FindBin::Bin/../appengine";

        # child
        my @args = ($dev_appserver,
                    "--clear_datastore",  # kinda redundant as we made a temp dir
                    "--datastore_path=$datadir",
                    "--blobstore_path=$blobdir",
                    "--port=$port",
                    $appdir);
        print STDERR "# Running: [@args]\n";
        exec @args;
        die "failed to exec: $!\n";
    }
    $self->{pid} = $pid;

    for (1..15) {
        print STDERR "# Waiting for appengine app to start...\n";
        my $res = $self->ua->request($self->get("/"));
        if ($res && $res->is_success) {
            print STDERR "# Up.";
            last;
        }
        sleep(1);
    }
    return 1;
}

sub DESTROY {
    my $self = shift;
    print STDERR "# Sending SIGQUIT to $self->{pid}\n";
    kill 3, $self->{pid} if $self->{pid};
}

1;



