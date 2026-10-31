#!/bin/bash
# build.sh builds the Perkeep iOS app without an Xcode project.
#
# Usage: clients/mobile/ios/build.sh sim|device
#
# It needs macOS with Xcode installed (for the iOS SDK and toolchain) and
# Go, but no Apple credentials. Simulator builds are ad-hoc signed and
# ready to run. Device builds are left unsigned; sign them on a machine
# with credentials using dev/iossign.
#
# Output: clients/mobile/ios/build/<sim|device>/Perkeep.app

set -euo pipefail

TARGET="${1:-}"
case "$TARGET" in
sim)
	SDK=iphonesimulator
	TRIPLE_SUFFIX=-simulator
	PLATFORM=iPhoneSimulator
	;;
device)
	SDK=iphoneos
	TRIPLE_SUFFIX=
	PLATFORM=iPhoneOS
	;;
*)
	echo "usage: $0 sim|device" >&2
	exit 2
	;;
esac

MIN_IOS=17.0
TRIPLE="arm64-apple-ios${MIN_IOS}${TRIPLE_SUFFIX}"

IOS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$IOS_DIR/../../.." && pwd)"
OUT="$IOS_DIR/build/$TARGET"
APP="$OUT/Perkeep.app"
SDK_PATH="$(xcrun --sdk "$SDK" --show-sdk-path)"

rm -rf "$OUT"
mkdir -p "$APP"

echo "==> Building Go core ($TRIPLE)"
(
	cd "$REPO_ROOT"
	CC="$(xcrun --sdk "$SDK" --find clang)" \
	CGO_CFLAGS="-isysroot $SDK_PATH -target $TRIPLE" \
	CGO_LDFLAGS="-isysroot $SDK_PATH -target $TRIPLE" \
	CGO_ENABLED=1 GOOS=ios GOARCH=arm64 \
	go build -buildmode=c-archive -trimpath -ldflags="-s -w" \
		-o "$OUT/libpkcore.a" ./clients/mobile/core
)

echo "==> Compiling Swift"
xcrun --sdk "$SDK" swiftc \
	-target "$TRIPLE" \
	-sdk "$SDK_PATH" \
	-O -parse-as-library \
	-import-objc-header "$OUT/libpkcore.h" \
	"$IOS_DIR"/Sources/*.swift \
	"$OUT/libpkcore.a" \
	-lresolv -framework CoreFoundation -framework Security \
	-o "$APP/Perkeep"

echo "==> Compiling asset catalog"
xcrun actool "$IOS_DIR/Assets.xcassets" \
	--compile "$APP" \
	--platform "$SDK" \
	--minimum-deployment-target "$MIN_IOS" \
	--target-device iphone --target-device ipad \
	--app-icon AppIcon \
	--output-partial-info-plist "$OUT/assets-Info.plist" \
	--errors --warnings --output-format human-readable-text

echo "==> Writing Info.plist"
PLIST="$APP/Info.plist"
cp "$IOS_DIR/Info.plist" "$PLIST"
# Add the icon keys (CFBundleIcons) that actool generated.
/usr/libexec/PlistBuddy -c "Merge $OUT/assets-Info.plist" "$PLIST"
# Keys that Xcode normally adds and App Store validation expects.
plutil -insert MinimumOSVersion -string "$MIN_IOS" "$PLIST"
plutil -insert CFBundleSupportedPlatforms -json "[\"$PLATFORM\"]" "$PLIST"
plutil -insert DTPlatformName -string "$SDK" "$PLIST"
plutil -insert DTPlatformVersion -string "$(xcrun --sdk "$SDK" --show-sdk-version)" "$PLIST"
plutil -insert DTSDKName -string "$SDK$(xcrun --sdk "$SDK" --show-sdk-version)" "$PLIST"
plutil -insert DTSDKBuild -string "$(xcrun --sdk "$SDK" --show-sdk-build-version)" "$PLIST"
plutil -insert DTXcode -string "$(xcodebuild -version | awk '/^Xcode/ {split($2, v, "."); printf "%d%d%d", v[1], v[2], v[3]}')" "$PLIST"
plutil -insert DTXcodeBuild -string "$(xcodebuild -version | awk '/^Build version/ {print $3}')" "$PLIST"
plutil -convert binary1 "$PLIST"
printf 'APPL????' >"$APP/PkgInfo"

if [ "$TARGET" = sim ]; then
	codesign --force --sign - --timestamp=none "$APP"
fi

echo "==> Built $APP"
