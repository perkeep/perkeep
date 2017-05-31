#!/usr/bin/perl

use strict;
use FindBin qw($Bin);

my $props = "$Bin/local.properties";
unless (-e $props) {
    die "\n".
        "**************************************************************\n".
        "Can't build the Camlistore Android client; SDK not configured.\n".
        "If you have the android SDK installed, you need to create your\n".
        "$props file,\n".
        "and set sdk.dir. See local.properties.TEMPLATE for instructions.\n".
        "Otherwise, run 'make dockerdebug', or 'make dockerrelease', to\n".
        "build in docker.\n".
        "**************************************************************\n\n";
}
