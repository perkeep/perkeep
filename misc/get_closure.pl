use strict;
use FindBin qw($Bin);
use File::Fetch;
use IO::Uncompress::Unzip qw(unzip $UnzipError) ;

my $closure_rev = "r2459";
my $closure_svn = "http://closure-library.googlecode.com/svn/trunk/";
my $compiler_version = "20121212";
my $compiler_baseurl = "http://closure-compiler.googlecode.com/files/";

sub get_closure_lib {
	my $closure_dir = "$Bin/tmp/closure-lib";
	if (-d $closure_dir) {
		chdir $closure_dir or die;
		my $local_rev = "r" . `svnversion`;
		chomp($local_rev);
		if ($local_rev ne $closure_rev) {
			die "No 'svn' found; install Subversion.\n" unless `which svn` =~ /\S/;
			system("svn", "update", "-r", $closure_rev)
				and die "Failed to svn up the closure library: $!\n";
		}
	} else {
		die "No 'svn' found; install Subversion.\n" unless `which svn` =~ /\S/;
		system("svn", "checkout", "-r", $closure_rev, $closure_svn, $closure_dir)
			and die "Failed to svn co the closure library: $!\n";
	}
}

sub get_closure_compiler {
	# first java check is needed, because the actual call
	# always has a non zero exit status (because running the
	# compiler.jar with --version writes to stderr).
	my $version = `java -version 2>/dev/null`;
	die "The Java Runtime Environment is needed to run the closure compiler.\n" if $?;
	my $closure_compiler_dir = "$Bin/tmp/closure-compiler";
	my $jar = "$closure_compiler_dir/compiler.jar";
	if (-f $jar) {
		my $cmd = join "", "java -jar ", $jar, " --version --help 2>&1";
		my $version = `$cmd`;
		$version =~ s/.*Version: (.*) \(revision.*/$1/s;
		if ($version eq $compiler_version) {
			return;
		}
		unlink $jar or die "Could not unlink $jar: $!";
	}
	printf("Getting closure compiler version %s.\n", $compiler_version);
	unless (-d $closure_compiler_dir) {
		system("mkdir", "-p", $closure_compiler_dir)
			and die "Failed to create $closure_compiler_dir.\n";
	}
	chdir $closure_compiler_dir or die;
	my $compiler_filename = join "", "compiler-", $compiler_version, ".zip";
	my $compiler_url = $compiler_baseurl . $compiler_filename;
	my $ff = File::Fetch->new(uri => $compiler_url);
	my $where = $ff->fetch() or die $ff->error;
	unzip $compiler_filename => "compiler.jar"
		or die "unzip failed: $UnzipError\n";
	unlink $compiler_filename or die "Could not unlink $compiler_filename: $!";
	return;
}

1;
