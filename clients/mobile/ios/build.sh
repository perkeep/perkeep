#!/bin/bash
# build.sh builds the Perkeep iOS app, with its share extension, without
# an Xcode project. The two share the Go core, as a framework.
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
EXT="$APP/PlugIns/PerkeepShare.appex"
FW="$APP/Frameworks/PKCore.framework"
SDK_PATH="$(xcrun --sdk "$SDK" --show-sdk-path)"

rm -rf "$OUT"
mkdir -p "$APP" "$EXT" "$FW"

echo "==> Building Go core ($TRIPLE)"
(
	cd "$REPO_ROOT"
	# Omit the tailscale.com features tailcat doesn't use, as tailcat's
	# own releases do; see its build-tags.md. go list reports no Dir for
	# a module not yet downloaded, so download it first.
	go mod download github.com/tailscale/tailcat
	TAGS="$(cat "$(go list -m -f '{{.Dir}}' github.com/tailscale/tailcat)/build-tags.txt")"
	CC="$(xcrun --sdk "$SDK" --find clang)" \
	CGO_CFLAGS="-isysroot $SDK_PATH -target $TRIPLE" \
	CGO_LDFLAGS="-isysroot $SDK_PATH -target $TRIPLE" \
	CGO_ENABLED=1 GOOS=ios GOARCH=arm64 \
	go build -buildmode=c-archive -trimpath -ldflags="-s -w" -tags="$TAGS" \
		-o "$OUT/libpkcore.a" ./clients/mobile/core
)

echo "==> Linking the Go core into a framework"
# Go can't build shared libraries for iOS (-buildmode=c-shared), so link
# its static library into one, exporting only the core's C API.
printf '_PKCall\n_PKFree\n' >"$OUT/exports.txt"
xcrun --sdk "$SDK" clang -dynamiclib \
	-target "$TRIPLE" -isysroot "$SDK_PATH" \
	-Wl,-all_load "$OUT/libpkcore.a" \
	-Wl,-exported_symbols_list,"$OUT/exports.txt" \
	-install_name @rpath/PKCore.framework/PKCore \
	-lresolv -framework CoreFoundation -framework Security \
	-o "$FW/PKCore"

# simEntitlements writes the entitlements of a simulator build of the
# bundle with ID id, and prints linker flags embedding them, as Xcode does
# for the simulator, rather than signing with them. The app and the
# extension share a keychain access group, as on a device; see
# dev/iossign.
simEntitlements() {
	[ "$TARGET" = sim ] || return 0
	local id="$1" f="$OUT/$1.entitlements"
	cat >"$f" <<EOF2
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
	<key>application-identifier</key><string>SIMULATOR.$id</string>
	<key>keychain-access-groups</key><array><string>SIMULATOR.org.perkeep.mobile</string></array>
</dict></plist>
EOF2
	echo -Xlinker -sectcreate -Xlinker __TEXT -Xlinker __entitlements -Xlinker "$f"
}

# swift compiles and links Swift sources with the Go core's framework.
swift() {
	xcrun --sdk "$SDK" swiftc \
		-target "$TRIPLE" \
		-sdk "$SDK_PATH" \
		-O -parse-as-library \
		-import-objc-header "$OUT/libpkcore.h" \
		"$@" \
		"$FW/PKCore"
}

echo "==> Compiling the app"
swift -module-name Perkeep "$IOS_DIR"/Sources/*.swift \
	-Xlinker -rpath -Xlinker @executable_path/Frameworks \
	$(simEntitlements org.perkeep.mobile) \
	-o "$APP/Perkeep"

echo "==> Compiling the share extension"
# The extension shares the app's code for the core, keychain, and
# accounts. Its executable starts at NSExtensionMain, as Xcode links
# extensions.
swift -module-name PerkeepShare -application-extension \
	"$IOS_DIR"/ShareExtension/*.swift \
	"$IOS_DIR"/Sources/{Core,Keychain,Accounts,SharedAccounts}.swift \
	-Xlinker -e -Xlinker _NSExtensionMain \
	-Xlinker -rpath -Xlinker @executable_path/../../Frameworks \
	$(simEntitlements org.perkeep.mobile.share) \
	-o "$EXT/PerkeepShare"

echo "==> Compiling asset catalog"
xcrun actool "$IOS_DIR/Assets.xcassets" \
	--compile "$APP" \
	--platform "$SDK" \
	--minimum-deployment-target "$MIN_IOS" \
	--target-device iphone --target-device ipad \
	--app-icon AppIcon \
	--output-partial-info-plist "$OUT/assets-Info.plist" \
	--errors --warnings --output-format human-readable-text

# plist writes the bundle's Info.plist from src, with the keys Xcode
# normally adds and App Store validation expects.
plist() {
	local src="$1" dst="$2"
	cp "$src" "$dst"
	plutil -insert MinimumOSVersion -string "$MIN_IOS" "$dst"
	plutil -insert CFBundleSupportedPlatforms -json "[\"$PLATFORM\"]" "$dst"
	plutil -insert DTPlatformName -string "$SDK" "$dst"
	plutil -insert DTPlatformVersion -string "$(xcrun --sdk "$SDK" --show-sdk-version)" "$dst"
	plutil -insert DTSDKName -string "$SDK$(xcrun --sdk "$SDK" --show-sdk-version)" "$dst"
	plutil -insert DTSDKBuild -string "$(xcrun --sdk "$SDK" --show-sdk-build-version)" "$dst"
	plutil -insert DTXcode -string "$(xcodebuild -version | awk '/^Xcode/ {split($2, v, "."); printf "%d%d%d", v[1], v[2], v[3]}')" "$dst"
	plutil -insert DTXcodeBuild -string "$(xcodebuild -version | awk '/^Build version/ {print $3}')" "$dst"
}

echo "==> Writing Info.plists"
plist "$IOS_DIR/Info.plist" "$APP/Info.plist"
# Add the icon keys (CFBundleIcons) that actool generated.
/usr/libexec/PlistBuddy -c "Merge $OUT/assets-Info.plist" "$APP/Info.plist"
plutil -convert binary1 "$APP/Info.plist"
printf 'APPL????' >"$APP/PkgInfo"
plist "$IOS_DIR/ShareExtension/Info.plist" "$EXT/Info.plist"
plutil -convert binary1 "$EXT/Info.plist"
plist "$IOS_DIR/PKCore-Info.plist" "$FW/Info.plist"
plutil -convert binary1 "$FW/Info.plist"

if [ "$TARGET" = sim ]; then
	codesign --force --sign - --timestamp=none "$FW"
	codesign --force --sign - --timestamp=none "$EXT"
	codesign --force --sign - --timestamp=none "$APP"
fi

echo "==> Built $APP"
