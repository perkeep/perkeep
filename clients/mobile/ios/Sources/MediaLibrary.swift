import Photos
import UIKit

/// MediaItem is one file to back up: a resource of a photo library asset,
/// such as a photo's original, its edited version, or a Live Photo's video.
struct MediaItem: Identifiable {
    /// id is the item's key in the core's caches. It changes whenever the
    /// file's contents might: an asset's originals never change, but its
    /// edited versions do, with the asset's modification date.
    let id: String
    let assetID: String
    let resource: PHAssetResource
    let name: String
    let size: Int64 // 0 if unknown
    let date: Date? // when the photo or video was taken
}

/// MediaLibrary finds the media to back up.
enum MediaLibrary {
    /// The resource types of originals, and of edited versions.
    private static let originalTypes: Set<PHAssetResourceType> = [.photo, .video, .pairedVideo, .alternatePhoto, .audio]
    private static let editedTypes: Set<PHAssetResourceType> = [.fullSizePhoto, .fullSizeVideo, .fullSizePairedVideo]

    /// items returns the files of the assets that the account's album
    /// rules select, newest first. progress is called with the number of
    /// assets looked at so far.
    static func items(accountID: String, store: AccountStore, progress: @escaping @Sendable (Int) -> Void) async throws -> [MediaItem] {
        let albums = PhotoAlbums.list().map(\.album)
        let states = try await store.albumStates(accountID, albums: albums)
        let included = states.albums.filter { $0.state == .include }.map(\.album.id)
        let excluded = states.albums.filter { $0.state == .exclude }.map(\.album.id)
        return await Task.detached(priority: .userInitiated) {
            var excludedAssets = Set<String>()
            for c in collections(excluded) {
                PHAsset.fetchAssets(in: c, options: nil).enumerateObjects { a, _, _ in
                    excludedAssets.insert(a.localIdentifier)
                }
            }
            var seen = Set<String>()
            var assets: [PHAsset] = []
            for c in collections(included) {
                PHAsset.fetchAssets(in: c, options: nil).enumerateObjects { a, _, _ in
                    if !excludedAssets.contains(a.localIdentifier), seen.insert(a.localIdentifier).inserted {
                        assets.append(a)
                    }
                }
            }
            assets.sort { ($0.creationDate ?? .distantPast) > ($1.creationDate ?? .distantPast) }
            var items: [MediaItem] = []
            for (i, a) in assets.enumerated() {
                items += mediaItems(a)
                if i % 200 == 0 {
                    progress(i)
                }
            }
            progress(assets.count)
            return items
        }.value
    }

    private static func collections(_ ids: [String]) -> [PHAssetCollection] {
        guard !ids.isEmpty else { return [] }
        var out: [PHAssetCollection] = []
        PHAssetCollection.fetchAssetCollections(withLocalIdentifiers: ids, options: nil)
            .enumerateObjects { c, _, _ in out.append(c) }
        return out
    }

    /// mediaItems returns the files of asset a to back up: its originals,
    /// and its edited versions, if it's been edited.
    private static func mediaItems(_ a: PHAsset) -> [MediaItem] {
        let resources = PHAssetResource.assetResources(for: a)
        let base = resources.first { $0.type == .photo || $0.type == .video }
            .map { ($0.originalFilename as NSString).deletingPathExtension } ?? "IMG"
        var out: [MediaItem] = []
        for r in resources {
            let edited = editedTypes.contains(r.type)
            guard edited || originalTypes.contains(r.type) else { continue }
            let size = (r.value(forKey: "fileSize") as? NSNumber)?.int64Value ?? 0
            var name = r.originalFilename
            var key = "\(a.localIdentifier)|\(r.type.rawValue)|\(name)|\(size)"
            if edited {
                // Edited versions are all named like FullSizeRender.heic.
                let ext = (name as NSString).pathExtension
                name = "\(base) (Edited)" + (ext.isEmpty ? "" : ".\(ext)")
                key += "|\(a.modificationDate?.timeIntervalSince1970 ?? 0)"
            }
            out.append(MediaItem(id: key, assetID: a.localIdentifier, resource: r, name: name, size: size, date: a.creationDate))
        }
        return out
    }

    /// tempDir is where items are exported to, for the core to read.
    static let tempDir = FileManager.default.temporaryDirectory.appendingPathComponent("pkupload", isDirectory: true)

    /// clearTemp deletes leftover exported files.
    static func clearTemp() {
        try? FileManager.default.removeItem(at: tempDir)
        try? FileManager.default.createDirectory(at: tempDir, withIntermediateDirectories: true)
    }

    /// export writes the item's file to a new temporary file, downloading
    /// it from iCloud if needed, and returns its URL. The caller deletes it.
    static func export(_ item: MediaItem) async throws -> URL {
        let url = tempDir.appendingPathComponent(UUID().uuidString + "-" + item.name)
        let opts = PHAssetResourceRequestOptions()
        opts.isNetworkAccessAllowed = true
        try await withCheckedThrowingContinuation { (c: CheckedContinuation<Void, Error>) in
            PHAssetResourceManager.default().writeData(for: item.resource, toFile: url, options: opts) { err in
                if let err {
                    c.resume(throwing: err)
                } else {
                    c.resume()
                }
            }
        }
        return url
    }

    /// thumbnail returns a small image of the asset.
    static func thumbnail(assetID: String, size: CGFloat) async -> UIImage? {
        guard let a = PHAsset.fetchAssets(withLocalIdentifiers: [assetID], options: nil).firstObject else {
            return nil
        }
        let opts = PHImageRequestOptions()
        opts.deliveryMode = .opportunistic
        opts.isNetworkAccessAllowed = true
        opts.resizeMode = .fast
        let scale = await UIScreen.main.scale
        return await withCheckedContinuation { c in
            var resumed = false
            PHImageManager.default().requestImage(
                for: a, targetSize: CGSize(width: size * scale, height: size * scale),
                contentMode: .aspectFill, options: opts
            ) { img, info in
                // Opportunistic delivery may call back twice: degraded, then final.
                let degraded = (info?[PHImageResultIsDegradedKey] as? Bool) ?? false
                if !resumed && (!degraded || img != nil) {
                    resumed = true
                    c.resume(returning: img)
                }
            }
        }
    }
}
