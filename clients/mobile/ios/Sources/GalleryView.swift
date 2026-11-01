import Photos
import SwiftUI

/// GalleryView shows the photos and videos that an account's album rules
/// select, each marked with whether it's on the server, and lets the user
/// select some to upload.
struct GalleryView: View {
    @ObservedObject var model: UploadModel

    @State private var selecting = false
    @State private var selected: Set<String> = []

    private let columns = [GridItem(.adaptive(minimum: 90), spacing: 2)]

    var body: some View {
        ScrollView {
            LazyVGrid(columns: columns, spacing: 2) {
                ForEach(Array(model.galleryAssets.enumerated()), id: \.element) { i, id in
                    GalleryCell(assetID: id, status: model.status(id),
                                selecting: selecting, selected: selected.contains(id))
                        .accessibilityLabel("Photo \(i + 1)")
                        .onTapGesture {
                            if selecting {
                                if !selected.insert(id).inserted {
                                    selected.remove(id)
                                }
                            }
                        }
                }
            }
        }
        .overlay {
            if model.galleryAssets.isEmpty {
                if model.running {
                    ProgressView("Finding photos…")
                } else {
                    ContentUnavailableView("No Photos", systemImage: "photo.on.rectangle",
                                           description: Text("No photos are in the albums this account backs up."))
                }
            }
        }
        .safeAreaInset(edge: .top) {
            if model.running {
                HStack {
                    ProgressView().controlSize(.small)
                    Text(statusLine)
                        .font(.caption.monospacedDigit())
                        .foregroundStyle(.secondary)
                    Spacer()
                    if model.phase == .uploading {
                        Button("Pause") { model.pause(notice: "Paused.") }
                            .font(.caption)
                    }
                }
                .padding(.horizontal)
                .padding(.vertical, 6)
                .background(.bar)
            }
        }
        .safeAreaInset(edge: .bottom) {
            if selecting && !selected.isEmpty {
                Button {
                    model.upload(assets: selected)
                    selected = []
                    selecting = false
                } label: {
                    Label("Upload \(selected.count.formatted()) \(selected.count == 1 ? "Photo" : "Photos")",
                          systemImage: "icloud.and.arrow.up")
                        .frame(maxWidth: .infinity)
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
                .padding()
                .background(.bar)
            }
        }
        .navigationTitle("Photos")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            if selecting {
                ToolbarItem(placement: .topBarLeading) {
                    Button("Select Missing") {
                        selected = Set(model.galleryAssets.filter { model.status($0) == .missing })
                    }
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Cancel") {
                        selecting = false
                        selected = []
                    }
                }
            } else {
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Select") { selecting = true }
                }
            }
        }
        .confirmationDialog("Upload over cellular data?", isPresented: $model.confirmCellular, titleVisibility: .visible) {
            Button("Upload Anyway") { model.upload(confirmedCellular: true) }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("The phone isn't on Wi-Fi, and uploading photos and videos can use a lot of data.")
        }
        .onAppear {
            // Find out which are on the server, to mark them.
            model.findIfIdle()
        }
    }

    private var statusLine: String {
        let st = model.stats
        switch model.phase {
        case .counting:
            return "Counting \(st.assetsSeen.formatted()) of \(st.assetCount.formatted())"
        case .hashing, .finding:
            return "Checking: \(st.hashed.formatted()) of \(st.files.formatted()) files hashed, "
                + "\((st.onServer + st.missing).formatted()) checked"
        case .uploading:
            return "Uploading: \(st.toUpload.formatted()) files to go"
        case .idle:
            return ""
        }
    }
}

/// GalleryCell is a thumbnail of a photo or video, marked with whether
/// it's on the server and, while selecting, whether it's selected.
private struct GalleryCell: View {
    let assetID: String
    let status: UploadModel.AssetStatus
    let selecting: Bool
    let selected: Bool

    @State private var image: UIImage?

    var body: some View {
        Color.clear
            .aspectRatio(1, contentMode: .fit)
            .overlay {
                if let image {
                    Image(uiImage: image).resizable().scaledToFill()
                } else {
                    Rectangle().fill(.quaternary)
                }
            }
            .clipped()
            .overlay(alignment: .bottomTrailing) {
                switch status {
                case .uploaded:
                    badge("icloud.fill")
                case .missing:
                    badge("icloud.slash")
                case .unknown:
                    EmptyView()
                }
            }
            .overlay(alignment: .topTrailing) {
                if selecting {
                    Image(systemName: selected ? "checkmark.circle.fill" : "circle")
                        .font(.title3)
                        .symbolRenderingMode(.palette)
                        .foregroundStyle(.white, selected ? Color.accentColor : .black.opacity(0.3))
                        .padding(4)
                }
            }
            .opacity(selecting && selected ? 0.75 : 1)
            .contentShape(Rectangle())
            .accessibilityValue(status == .uploaded ? "uploaded" : status == .missing ? "not uploaded" : "")
            .task(id: assetID) {
                image = await Thumbnails.load(assetID, side: 180)
            }
    }

    private func badge(_ symbol: String) -> some View {
        Image(systemName: symbol)
            .font(.caption.bold())
            .foregroundStyle(.white)
            .shadow(color: .black.opacity(0.6), radius: 2)
            .padding(5)
    }
}

/// Thumbnails loads small images of photos and videos.
@MainActor
enum Thumbnails {
    private static let manager = PHCachingImageManager()

    /// load returns an image of the asset at least side points square.
    static func load(_ assetID: String, side: CGFloat) async -> UIImage? {
        guard let a = PHAsset.fetchAssets(withLocalIdentifiers: [assetID], options: nil).firstObject else {
            return nil
        }
        let opts = PHImageRequestOptions()
        opts.deliveryMode = .opportunistic
        opts.resizeMode = .fast
        opts.isNetworkAccessAllowed = true
        let px = side * UIScreen.main.scale
        return await withCheckedContinuation { c in
            var resumed = false
            manager.requestImage(for: a, targetSize: CGSize(width: px, height: px),
                                 contentMode: .aspectFill, options: opts) { img, info in
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
