# Perkeep mobile apps

Work in progress. The apps are native UIs around a shared Go core:

- `core/`: the Go core, built as a C archive (iOS) or shared library
  (Android). Native code calls it with HTTP-style requests through
  `PKCall`; see the package comment.
- `ios/`: the iOS app (SwiftUI), built by `ios/build.sh` without an
  Xcode project.

Building and signing are separate steps so that the build machine needs
no Apple credentials.

## Building (any Mac with Xcode and Go)

    clients/mobile/ios/build.sh sim      # ad-hoc signed, for the simulator
    clients/mobile/ios/build.sh device   # unsigned, for dev/iossign

To run the simulator build:

    xcrun simctl boot "iPhone 17 Pro"
    xcrun simctl install booted clients/mobile/ios/build/sim/Perkeep.app
    xcrun simctl launch booted org.perkeep.mobile

## Signing (a Mac with your Apple credentials)

`dev/iossign` signs the unsigned device build, then installs it on a
paired iPhone or uploads it to TestFlight. It needs Xcode installed (for
`devicectl` and `altool`) but never opens it.

One-time setup:

1. In [App Store Connect](https://appstoreconnect.apple.com), go to
   Users and Access → Integrations → App Store Connect API → Team Keys,
   generate a key, and download its `.p8` file (possible only once) to
   `~/.appstoreconnect/private_keys/AuthKey_<KeyID>.p8`.
2. Write `~/Library/Application Support/perkeep/iossign.json` with your
   Team ID (from developer.apple.com → Membership details), the key's Key
   ID and Issuer ID, and the bundle ID to use. See `go doc ./dev/iossign`.
3. Connect your iPhone by USB once and tap Trust. After the first install
   attempt, enable Settings → Privacy & Security → Developer Mode.

Then, with the device build copied from the build machine:

    go run ./dev/iossign install Perkeep.app
    go run ./dev/iossign upload Perkeep.app

`make iossign` copies the device build from `IOS_BUILD_HOST` and runs
`iossign install` on it.

On first use, iossign registers the bundle IDs (the app's, and its share
extension's, which is the app's with `.share` added) and the device,
creates an Apple signing certificate whose private key stays in your
login keychain, and creates provisioning profiles. Uploading also needs
an app record for the bundle ID in App Store Connect (Apps → +).

The share extension, which uploads photos shared from other apps, can't
read the app's files, so it has its own Go core and finds the app's
accounts in the keychain, which the two share.
