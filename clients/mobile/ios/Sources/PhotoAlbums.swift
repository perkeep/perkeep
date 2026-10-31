import Photos

/// PhotoAlbums lists the device's photo albums.
enum PhotoAlbums {
    struct Info {
        var album: Album
        var count: Int? // estimated number of items, if known
    }

    static func requestAccess() async -> PHAuthorizationStatus {
        await PHPhotoLibrary.requestAuthorization(for: .readWrite)
    }

    /// list returns the Camera Roll followed by the user's albums, sorted
    /// by title.
    static func list() -> [Info] {
        var cameraRoll: [Info] = []
        PHAssetCollection.fetchAssetCollections(with: .smartAlbum, subtype: .smartAlbumUserLibrary, options: nil)
            .enumerateObjects { c, _, _ in
                cameraRoll.append(info(c, title: "Camera Roll", cameraRoll: true))
            }
        var albums: [Info] = []
        PHAssetCollection.fetchAssetCollections(with: .album, subtype: .any, options: nil)
            .enumerateObjects { c, _, _ in
                albums.append(info(c, title: c.localizedTitle ?? "Untitled", cameraRoll: false))
            }
        albums.sort { $0.album.title.localizedStandardCompare($1.album.title) == .orderedAscending }
        return cameraRoll + albums
    }

    private static func info(_ c: PHAssetCollection, title: String, cameraRoll: Bool) -> Info {
        var count: Int? = c.estimatedAssetCount
        if count == NSNotFound {
            count = PHAsset.fetchAssets(in: c, options: nil).count
        }
        return Info(album: Album(id: c.localIdentifier, title: title, cameraRoll: cameraRoll), count: count)
    }
}
