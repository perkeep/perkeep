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
    let resourceType: PHAssetResourceType
    let originalFilename: String // of the resource, to find it to export
    let name: String             // to upload it as
    let size: Int64              // 0 if unknown
    let date: Date?              // when the photo or video was taken
}

/// MediaCount is the result of counting the media to back up.
struct MediaCount {
    var assets = 0   // photos and videos
    var files = 0    // their files
    var lookedUp = 0 // assets whose files weren't in the cache
}

/// MediaLibrary finds the media to back up.
enum MediaLibrary {
    /// The resource types of originals, and of edited versions.
    private static let originalTypes: Set<PHAssetResourceType> = [.photo, .video, .pairedVideo, .alternatePhoto, .audio]
    private static let editedTypes: Set<PHAssetResourceType> = [.fullSizePhoto, .fullSizeVideo, .fullSizePairedVideo]

    /// countBatch is how many assets' files count passes to onBatch at once.
    private static let countBatch = 200

    /// count finds the files of the assets that the account's album rules
    /// select, newest first, passing them to onBatch as it goes, so later
    /// work can start before counting is done. onStart is called with the
    /// number of assets, once known.
    static func count(accountID: String, store: AccountStore,
                      onStart: @MainActor (Int) -> Void,
                      onBatch: @MainActor ([MediaItem], _ assetsDone: Int) async throws -> Void) async throws -> MediaCount {
        let albums = PhotoAlbums.list().map(\.album)
        let states = try await store.albumStates(accountID, albums: albums)
        let included = states.albums.filter { $0.state == .include }.map(\.album.id)
        let excluded = states.albums.filter { $0.state == .exclude }.map(\.album.id)
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
        await onStart(assets.count)
        var cache = ResourceCache.load()
        var res = MediaCount(assets: assets.count)
        var batch: [MediaItem] = []
        for (i, a) in assets.enumerated() {
            try Task.checkCancellation()
            let (items, cached) = mediaItems(a, cache: &cache)
            batch += items
            if !cached {
                res.lookedUp += 1
            }
            if (i + 1) % countBatch == 0 || i == assets.count - 1 {
                res.files += batch.count
                try await onBatch(batch, i + 1)
                batch = []
            }
        }
        // Counting finished, so the cache can forget assets no longer here.
        cache.prune(keeping: seen)
        if res.lookedUp > 0 || cache.pruned {
            cache.save()
        }
        return res
    }

    private static func collections(_ ids: [String]) -> [PHAssetCollection] {
        guard !ids.isEmpty else { return [] }
        var out: [PHAssetCollection] = []
        PHAssetCollection.fetchAssetCollections(withLocalIdentifiers: ids, options: nil)
            .enumerateObjects { c, _, _ in out.append(c) }
        return out
    }

    /// mediaItems returns the files of asset a to back up: its originals,
    /// and its edited versions, if it's been edited. It reports whether
    /// they came from the cache.
    private static func mediaItems(_ a: PHAsset, cache: inout ResourceCache) -> ([MediaItem], Bool) {
        let mod = a.modificationDate?.timeIntervalSince1970 ?? 0
        var rs = cache.lookup(a.localIdentifier, modified: mod)
        let cached = rs != nil
        if rs == nil {
            // Asking Photos for an asset's resources is the slow part of
            // counting, so they're cached.
            let found = PHAssetResource.assetResources(for: a).compactMap { r -> ResourceCache.Resource? in
                guard editedTypes.contains(r.type) || originalTypes.contains(r.type) else { return nil }
                let size = (r.value(forKey: "fileSize") as? NSNumber)?.int64Value ?? 0
                return ResourceCache.Resource(type: r.type.rawValue, name: r.originalFilename, size: size)
            }
            cache.store(a.localIdentifier, modified: mod, found)
            rs = found
        }
        let resources = rs ?? []
        let base = resources.first {
            $0.type == PHAssetResourceType.photo.rawValue || $0.type == PHAssetResourceType.video.rawValue
        }.map { ($0.name as NSString).deletingPathExtension } ?? "IMG"
        var out: [MediaItem] = []
        for r in resources {
            guard let type = PHAssetResourceType(rawValue: r.type) else { continue }
            var name = r.name
            var key = "\(a.localIdentifier)|\(r.type)|\(r.name)|\(r.size)"
            if editedTypes.contains(type) {
                // Edited versions are all named like FullSizeRender.heic.
                let ext = (name as NSString).pathExtension
                name = "\(base) (Edited)" + (ext.isEmpty ? "" : ".\(ext)")
                key += "|\(mod)"
            }
            out.append(MediaItem(id: key, assetID: a.localIdentifier, resourceType: type, originalFilename: r.name,
                                 name: name, size: r.size, date: a.creationDate))
        }
        return (out, cached)
    }

    /// resource returns the item's PHAssetResource.
    static func resource(_ item: MediaItem) throws -> PHAssetResource {
        guard let a = PHAsset.fetchAssets(withLocalIdentifiers: [item.assetID], options: nil).firstObject,
              let r = PHAssetResource.assetResources(for: a).first(where: {
                  $0.type == item.resourceType && $0.originalFilename == item.originalFilename
              })
        else {
            throw Core.Error(status: 0, message: "\(item.name) is no longer in the photo library.")
        }
        return r
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
        // writeData fails (PHPhotosErrorDomain -1) if the directory
        // doesn't exist.
        try FileManager.default.createDirectory(at: tempDir, withIntermediateDirectories: true)
        let url = tempDir.appendingPathComponent(UUID().uuidString + "-" + item.name)
        let resource = try resource(item)
        let opts = PHAssetResourceRequestOptions()
        opts.isNetworkAccessAllowed = true
        try await withCheckedThrowingContinuation { (c: CheckedContinuation<Void, Error>) in
            PHAssetResourceManager.default().writeData(for: resource, toFile: url, options: opts) { err in
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

/// ResourceCache remembers the resources (files) of photo library assets,
/// by asset ID, as asking Photos for them takes a few milliseconds per
/// asset. An entry is used only while the asset's modification date is
/// unchanged, as editing an asset adds or changes resources.
struct ResourceCache: Codable {
    struct Resource: Codable {
        var type: Int // PHAssetResourceType
        var name: String
        var size: Int64
    }

    struct Entry: Codable {
        var modified: Double // the asset's modification date, as a Unix time
        var resources: [Resource]
    }

    private var entries: [String: Entry] = [:]
    private(set) var pruned = false // whether prune removed any

    private enum CodingKeys: String, CodingKey { case entries }

    /// prune forgets the assets not in keep.
    mutating func prune(keeping keep: Set<String>) {
        let n = entries.count
        entries = entries.filter { keep.contains($0.key) }
        pruned = entries.count != n
    }

    private static var url: URL {
        FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("asset-resources.json")
    }

    static func load() -> ResourceCache {
        guard let data = try? Data(contentsOf: url),
              let c = try? JSONDecoder().decode(ResourceCache.self, from: data)
        else {
            return ResourceCache()
        }
        return c
    }

    func save() {
        if let data = try? JSONEncoder().encode(self) {
            try? data.write(to: Self.url, options: .atomic)
        }
    }

    func lookup(_ assetID: String, modified: Double) -> [Resource]? {
        guard let e = entries[assetID], e.modified == modified else { return nil }
        return e.resources
    }

    mutating func store(_ assetID: String, modified: Double, _ rs: [Resource]) {
        entries[assetID] = Entry(modified: modified, resources: rs)
    }
}
