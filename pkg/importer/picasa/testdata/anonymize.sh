#!/bin/sh -e
#
# To save the picasa dialogue, start your perkeepd with
# PICAGO_DEBUG_DIR=/path/to/save/to
# CAMLI_PICASA_FULL_IMPORT=1
#
xmllint --pretty 1 - \
	| sed -e 's/110475319045955272364/99999999999999999999/g' \
	| sed -e "s:googleusercontent.com/[^<_\"']*:googleusercontent.com/REDACTED:g" \
	| sed -e "s:authkey=[^\"']*:authkey=AUTHKEY:g"

