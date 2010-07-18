#!/bin/sh

curl -u foo:foo -F sha1-7c63f74bbe0b1de55ec41ad0d9297a3762ecfdbc=@/bin/true http://127.0.0.1:3179/camli/upload

