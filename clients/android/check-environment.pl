#!/usr/bin/perl

use strict;
use FindBin qw($Bin);

my $props = "$Bin/local.properties";
unless (-e $props) {
    die "\n".
        "**************************************************************\n".
        "Can't build the Camlistore Android client; SDK not configured.\n".
        "You need to create your $props file.\n".
        "See local.properties.TEMPLATE for instructions.\n".
        "**************************************************************\n\n";
}
