import SwiftUI

/// UploadView backs up an account's photos and videos, with a button for
/// each phase, live stats, and the items in flight.
struct UploadView: View {
    @StateObject private var model: UploadModel

    init(accountID: String, store: AccountStore) {
        _model = StateObject(wrappedValue: UploadModel(accountID: accountID, store: store))
    }

    var body: some View {
        List {
            if let n = model.notice {
                Section {
                    Label(n, systemImage: "exclamationmark.triangle")
                        .foregroundStyle(.orange)
                }
            }
            overall
            buttons
            if !model.inFlight.isEmpty {
                Section("In Flight") {
                    ForEach(model.inFlight) { f in
                        InFlightRow(f: f)
                    }
                }
            }
            stats
        }
        .navigationTitle("Upload Photos")
        .navigationBarTitleDisplayMode(.inline)
        .animation(.default, value: model.inFlight.map(\.id))
        .confirmationDialog("Upload over cellular data?", isPresented: $model.confirmCellular, titleVisibility: .visible) {
            Button("Upload Anyway") { model.upload(confirmedCellular: true) }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("The phone isn't on Wi-Fi, and uploading photos and videos can use a lot of data.")
        }
        .onAppear {
            // For testing in the simulator, which can't tap.
            if UserDefaults.standard.bool(forKey: "PKAutoUpload") {
                model.upload(confirmedCellular: true)
            }
        }
        .onDisappear { model.pause() }
    }

    private var overall: some View {
        Section {
            VStack(alignment: .leading, spacing: 8) {
                HStack {
                    if model.running {
                        ProgressView().controlSize(.small)
                    }
                    Text(model.running ? model.phase.rawValue + "…" : (model.counted ? "Ready" : "Not counted yet"))
                        .font(.headline)
                    Spacer()
                    if model.phase == .uploading && model.bytesPerSecond > 0 {
                        Text(Self.bytes(Int64(model.bytesPerSecond)) + "/s")
                            .font(.subheadline.monospacedDigit())
                            .foregroundStyle(.secondary)
                            .contentTransition(.numericText())
                    }
                }
                if let p = progress {
                    ProgressView(value: p.value, total: max(p.total, 1))
                    Text(p.label)
                        .font(.caption.monospacedDigit())
                        .foregroundStyle(.secondary)
                        .contentTransition(.numericText())
                }
            }
            .animation(.default, value: progress?.label)
        }
    }

    /// progress is the progress of the phase running, or else how many of
    /// the items are on the server, or nil if there's nothing to show.
    private var progress: (value: Double, total: Double, label: String)? {
        let n = model.items.count
        switch model.phase {
        case .counting where model.assetCount > 0:
            return (Double(model.assetsSeen), Double(model.assetCount),
                    "\(model.assetsSeen.formatted()) of \(model.assetCount.formatted()) photos and videos looked at")
        case .counting, .connecting:
            return nil
        case .hashing:
            return (Double(model.hashedBytes), Double(model.totalBytes),
                    "\(model.hashedCount.formatted()) of \(n.formatted()) files hashed · "
                        + "\(Self.bytes(model.hashedBytes)) of \(Self.bytes(model.totalBytes))")
        case .finding:
            let asked = model.onServer.count + model.missing.count
            return (Double(asked), Double(model.hashedCount),
                    "\(asked.formatted()) of \(model.hashedCount.formatted()) hashed files checked")
        case .idle, .uploading:
            guard model.counted, n > 0 else { return nil }
            let done = n - model.toUpload
            return (Double(done), Double(n), "\(done.formatted()) of \(n.formatted()) on the server")
        }
    }

    private var buttons: some View {
        Section {
            if model.running {
                Button {
                    model.pause(notice: "Paused.")
                } label: {
                    Label("Pause", systemImage: "pause.fill")
                }
            } else {
                Button { model.upload() } label: {
                    Label("Upload", systemImage: "icloud.and.arrow.up.fill")
                        .font(.headline)
                }
                Button { model.countLocal() } label: {
                    Label("Count Local", systemImage: "number")
                }
                Button { model.hashLocal() } label: {
                    Label("Hash Local", systemImage: "number.square")
                }
                Button { model.findMissing() } label: {
                    Label("Find Missing", systemImage: "questionmark.folder")
                }
            }
        } footer: {
            if !model.running {
                Text("Upload counts, hashes, and finds what the server is missing as it goes. Hashes and what the server has are remembered, so later uploads only look at new photos.")
            }
        }
    }

    private var stats: some View {
        Section {
            Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 6) {
                statHeader("On this phone")
                GridRow {
                    Text("Photos & videos")
                    Self.number((model.phase == .counting ? model.assetsSeen : model.assetCount).formatted())
                    Text(model.phase == .counting && model.assetCount > 0 ? "of \(model.assetCount.formatted())" : "")
                        .foregroundStyle(.secondary)
                }
                stat("Files", model.items.count, model.totalBytes)
                Divider()
                statHeader("Hashing")
                stat("Hashed", model.hashedCount, model.hashedBytes)
                stat("To hash", model.items.count - model.hashedCount, model.unhashedBytes,
                     note: model.hashing > 0 ? "\(model.hashing) now" : nil)
                Divider()
                statHeader("Server")
                stat("Already there", model.onServer.count, nil)
                stat("Missing", model.missing.count, model.missingBytes)
                Divider()
                statHeader("Uploading")
                stat("Done", model.uploaded.count, model.uploadedBytes)
                stat("In flight", model.inFlight.count, model.inFlight.reduce(0) { $0 + $1.item.size })
                stat("Remaining", model.toUpload, model.toUploadBytes)
                if model.failed > 0 {
                    stat("Failed", model.failed, nil)
                }
                GridRow {
                    Text("Sent")
                    Self.number(Self.bytes(model.totals.bytesUploaded))
                    Text("\(model.totals.chunksUploaded.formatted()) chunks")
                        .foregroundStyle(.secondary)
                }
                GridRow {
                    Text("Skipped")
                    Self.number("")
                    Text("\(model.totals.chunksPresent.formatted()) chunks there")
                        .foregroundStyle(.secondary)
                }
            }
            .font(.subheadline.monospacedDigit())
        } footer: {
            if model.counted {
                Text("Files include Live Photos' videos, and both versions of edited photos."
                     + (model.countSummary.map { " " + $0 + "." } ?? ""))
            }
        }
    }

    private func statHeader(_ s: String) -> some View {
        GridRow {
            Text(s).font(.caption.bold()).foregroundStyle(.secondary).textCase(.uppercase)
                .gridCellColumns(3)
        }
    }

    /// stat is a grid row of a count and, if known, their bytes.
    private func stat(_ label: String, _ n: Int, _ bytes: Int64?, note: String? = nil) -> some View {
        GridRow {
            Text(label)
            Self.number(n.formatted())
            Text([bytes.map(Self.bytes), note].compactMap { $0 }.joined(separator: " · "))
                .foregroundStyle(.secondary)
        }
    }

    /// number is a grid cell of a number, right-aligned in a column as wide
    /// as the widest number expected, so the grid's columns don't move as
    /// the numbers change.
    private static func number(_ s: String) -> some View {
        ZStack(alignment: .trailing) {
            Text("888,888").hidden()
            Text("888.8 MB").hidden()
            Text(s)
                .contentTransition(.numericText())
                .animation(.default, value: s)
        }
        .gridColumnAlignment(.trailing)
    }

    static func bytes(_ n: Int64) -> String {
        ByteCountFormatter.string(fromByteCount: n, countStyle: .file)
    }
}

/// InFlightRow is an item being uploaded: its thumbnail, name, and
/// progress.
private struct InFlightRow: View {
    let f: UploadModel.InFlight

    var body: some View {
        HStack(spacing: 12) {
            Group {
                if let img = f.thumbnail {
                    Image(uiImage: img).resizable().scaledToFill()
                } else {
                    Rectangle().fill(.quaternary)
                }
            }
            .frame(width: 44, height: 44)
            .clipShape(RoundedRectangle(cornerRadius: 6))
            VStack(alignment: .leading, spacing: 4) {
                HStack {
                    Text(f.item.name).lineLimit(1).truncationMode(.middle)
                    Spacer()
                    Text(UploadView.bytes(f.item.size))
                        .font(.caption.monospacedDigit())
                        .foregroundStyle(.secondary)
                }
                ProgressView(value: f.fraction)
                    .animation(.linear(duration: 0.25), value: f.fraction)
                Text(f.phase.capitalized)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
    }
}
