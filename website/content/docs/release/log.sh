#!/bin/bash

PREV=0.8
echo "$(git shortlog -e -s $PREV..HEAD | sed -e 's/^.*<//;s/>.*$//' | uniq | wc -l) total commiters over $(git log --format=oneline $PREV..HEAD | wc -l) commits since $PREV (), including $(i=0; git shortlog -s $PREV..HEAD | cut -c8- | fgrep ' ' | while read nm; do i=$(($i + 1)); if [ $i -ge 2 ]; then echo -n ', '; fi; echo -n "$nm"; done)."
