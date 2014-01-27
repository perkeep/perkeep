#!/bin/sh

#  make-dmg.sh
#  Created by Dustin Sallings on 2014/1/18.
#  Copyright (c) 2014 Camlistore. All rights reserved.

set -ex

dir="$TARGET_TEMP_DIR/disk"
dmg="$BUILT_PRODUCTS_DIR/$PROJECT_NAME.dmg"

rm -rf "$dir"
mkdir -p "$dir"
cp -R "$BUILT_PRODUCTS_DIR/$PROJECT_NAME.app" "$dir"
cp -R "$PROJECT_DIR/../../../README" "$dir/README.txt"
cp -R "$PROJECT_DIR/../../../COPYING" "$dir/LICENSE.txt"
ln -s "/Applications" "$dir/Applications"
rm -f "$dmg"
hdiutil create -srcfolder "$dir" -volname "$PROJECT_NAME" "$dmg"
rm -rf "$dir"
