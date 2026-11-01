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
                .animation(.default, value: model.inFlight.map(\.id))
            }
            stats
        }
        .navigationTitle("Upload Photos")
        .navigationBarTitleDisplayMode(.inline)
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
                    if model.phase == .uploading && model.stats.bytesPerSecond > 0 {
                        Text(Self.bytes(Int64(model.stats.bytesPerSecond)) + "/s")
                            .font(.subheadline.monospacedDigit())
                            .foregroundStyle(.secondary)
                    }
                }
                if let p = progress {
                    ProgressView(value: p.value, total: max(p.total, 1))
                    Text(p.label)
                        .font(.caption.monospacedDigit())
                        .foregroundStyle(.secondary)
                }
            }
        }
    }

    /// progress is the progress of the phase running, or else how many of
    /// the items are on the server, or nil if there's nothing to show.
    private var progress: (value: Double, total: Double, label: String)? {
        let st = model.stats
        let n = st.files
        switch model.phase {
        case .counting where st.assetCount > 0:
            return (Double(st.assetsSeen), Double(st.assetCount),
                    "\(st.assetsSeen.formatted()) of \(st.assetCount.formatted()) photos and videos looked at")
        case .counting:
            return nil
        case .hashing:
            return (Double(st.hashedBytes), Double(st.totalBytes),
                    "\(st.hashed.formatted()) of \(n.formatted()) files hashed · "
                        + "\(Self.bytes(st.hashedBytes)) of \(Self.bytes(st.totalBytes))")
        case .finding:
            // Finding hashes what isn't hashed, too.
            let asked = st.onServer + st.missing
            return (Double(asked), Double(n),
                    "\(asked.formatted()) of \(n.formatted()) files checked · \(st.hashed.formatted()) hashed")
        case .idle, .uploading:
            guard model.counted, n > 0 else { return nil }
            let done = n - st.toUpload
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
            let st = model.stats
            Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 6) {
                statHeader("On this phone")
                GridRow {
                    Text("Photos & videos")
                    Self.number((model.phase == .counting ? st.assetsSeen : st.assetCount).formatted())
                    Text(model.phase == .counting && st.assetCount > 0 ? "of \(st.assetCount.formatted())" : "")
                        .foregroundStyle(.secondary)
                }
                stat("Files", st.files, st.totalBytes)
                Divider()
                statHeader("Hashing")
                stat("Hashed", st.hashed, st.hashedBytes)
                stat("To hash", st.files - st.hashed, st.totalBytes - st.hashedBytes)
                Divider()
                statHeader("Server")
                stat("Already there", st.onServer, nil)
                stat("Missing", st.missing, st.missingBytes)
                Divider()
                statHeader("Uploading")
                stat("Done", st.uploaded, st.uploadedBytes)
                stat("In flight", st.inFlight, st.inFlightBytes)
                stat("Remaining", st.toUpload, st.toUploadBytes)
                if st.failed > 0 {
                    stat("Failed", st.failed, nil)
                }
                GridRow {
                    Text("Sent")
                    Self.number(Self.bytes(st.totals.bytesUploaded))
                    Text("\(st.totals.chunksUploaded.formatted()) chunks")
                        .foregroundStyle(.secondary)
                }
                GridRow {
                    Text("Skipped")
                    Self.number("")
                    Text("\(st.totals.chunksPresent.formatted()) chunks there")
                        .foregroundStyle(.secondary)
                }
            }
            .font(.subheadline.monospacedDigit())
            // Fill the row, so text changing width in the last column
            // doesn't resize and move the grid.
            .frame(maxWidth: .infinity, alignment: .leading)
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
    private func stat(_ label: String, _ n: Int, _ bytes: Int64?) -> some View {
        GridRow {
            Text(label)
            Self.number(n.formatted())
            Text(bytes.map(Self.bytes) ?? "")
                .foregroundStyle(.secondary)
                .frame(maxWidth: .infinity, alignment: .leading)
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
