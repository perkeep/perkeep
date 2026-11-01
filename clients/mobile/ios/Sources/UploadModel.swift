import Network
import SwiftUI

/// UploadModel backs up an account's media: it finds the items the
/// account's album rules select, hashes them, asks the server which it
/// lacks, and uploads those, keeping stats as it goes. The Go core does
/// the hashing and uploading; see clients/mobile/core/uploads.go.
@MainActor
final class UploadModel: ObservableObject {
    enum Phase: String {
        case idle = "Ready"
        case connecting = "Connecting"
        case counting = "Counting"
        case hashing = "Hashing"
        case finding = "Finding missing"
        case uploading = "Uploading"
    }

    /// InFlight is an item being uploaded.
    struct InFlight: Identifiable {
        let item: MediaItem
        var id: String { item.id }
        var phase = "exporting"
        var fraction = 0.0 // of the file read for this phase
        var thumbnail: UIImage?
    }

    let accountID: String
    private let store: AccountStore

    @Published private(set) var phase = Phase.idle
    @Published private(set) var running = false
    @Published var notice: String? // why it stopped, or a warning
    @Published var confirmCellular = false

    // Counting.
    @Published private(set) var counted = false
    @Published private(set) var assetsSeen = 0
    @Published private(set) var items: [MediaItem] = []
    @Published private(set) var totalBytes: Int64 = 0

    // Hashing.
    @Published private(set) var refs: [String: [String]] = [:] // by item key
    @Published private(set) var hashedBytes: Int64 = 0
    @Published private(set) var hashing = 0 // items being hashed now

    // Finding missing.
    @Published private(set) var onServer: Set<String> = []
    @Published private(set) var missing: Set<String> = []

    // Uploading.
    @Published private(set) var uploaded: Set<String> = [] // by this run, or found on the server while running
    @Published private(set) var uploadedBytes: Int64 = 0 // of the items' sizes
    @Published private(set) var failed = 0
    @Published private(set) var inFlight: [InFlight] = []
    @Published private(set) var totals = Totals()
    @Published private(set) var bytesPerSecond = 0.0

    /// Totals are the core's counts of the connection's uploads.
    struct Totals: Decodable {
        var filesDone = 0
        var filesDup = 0
        var filesFailed = 0
        var bytesUploaded: Int64 = 0
        var chunks = 0
        var chunksPresent = 0
        var chunksUploaded = 0
    }

    private var work: Task<Void, Never>?
    private var connected = false
    private var waiters: [Int: CheckedContinuation<UploadDone, Never>] = [:] // by core upload ID
    private var finished: [Int: UploadDone] = [:] // by core upload ID, with no waiter yet
    private var rateSamples: [(Date, Int64)] = []

    private let monitor = NWPathMonitor()
    private var onCellular = false
    private var startedOnCellular = false

    /// maxUploads is how many items are uploaded at once.
    private static let maxUploads = 3
    private static let maxHashing = 2

    init(accountID: String, store: AccountStore) {
        self.accountID = accountID
        self.store = store
        monitor.pathUpdateHandler = { [weak self] path in
            let cell = path.usesInterfaceType(.cellular) || path.isExpensive
            Task { @MainActor in self?.networkChanged(onCellular: cell) }
        }
        monitor.start(queue: .main)
    }

    deinit {
        monitor.cancel()
    }

    var isOnCellular: Bool { onCellular }

    private func networkChanged(onCellular cell: Bool) {
        let was = onCellular
        onCellular = cell
        if running && cell && !was && !startedOnCellular {
            pause(notice: "Paused because the phone switched from Wi-Fi to cellular data.")
        }
    }

    // MARK: Stats

    var hashedCount: Int { items.lazy.filter { self.refs[$0.id] != nil }.count }
    var unhashedBytes: Int64 { items.lazy.filter { self.refs[$0.id] == nil }.reduce(0) { $0 + $1.size } }
    var missingBytes: Int64 { items.lazy.filter { self.missing.contains($0.id) }.reduce(0) { $0 + $1.size } }
    /// toUpload is how many items aren't known to be on the server yet.
    var toUpload: Int { items.count - onServer.count - uploaded.subtracting(onServer).count }
    var toUploadBytes: Int64 {
        items.lazy.filter { !self.onServer.contains($0.id) && !self.uploaded.contains($0.id) }.reduce(0) { $0 + $1.size }
    }

    // MARK: Actions

    func countLocal() { start(cellularOK: true) { try await self.count() } }
    func hashLocal() { start(cellularOK: true) { try await self.hashAll() } }
    func findMissing() { start(cellularOK: false) { try await self.findAll() } }

    /// upload uploads everything missing. On cellular data, it first asks.
    func upload(confirmedCellular: Bool = false) {
        if onCellular && !confirmedCellular {
            confirmCellular = true
            return
        }
        start(cellularOK: confirmedCellular) { try await self.uploadAll() }
    }

    func pause(notice: String? = nil) {
        work?.cancel()
        self.notice = notice
        Task.detached { _ = try? Core.send("DELETE", "/accounts/\(self.accountID)/uploads") }
    }

    private func start(cellularOK: Bool, _ fn: @escaping () async throws -> Void) {
        guard !running else { return }
        running = true
        notice = nil
        startedOnCellular = onCellular && cellularOK
        UIApplication.shared.isIdleTimerDisabled = true
        work = Task {
            do {
                try await fn()
            } catch is CancellationError {
            } catch {
                if !Task.isCancelled {
                    notice = error.localizedDescription
                }
            }
            running = false
            phase = .idle
            hashing = 0
            inFlight = []
            UIApplication.shared.isIdleTimerDisabled = false
        }
    }

    // MARK: Phases

    private func core<T: Decodable & Sendable>(_ method: String, _ path: String, _ body: (any Encodable & Sendable)? = nil) async throws -> T {
        let path = "/accounts/\(accountID)" + path
        return try await Task.detached { try Core.json(method, path, body) as T }.value
    }

    private func connect() async throws {
        guard !connected else { return }
        phase = .connecting
        guard let a = store.account(accountID) else { throw Core.Error(status: 404, message: "No such account.") }
        let s = Keychain.secrets(account: a.id) ?? Secrets()
        struct Resp: Decodable { var hashes: [String] }
        let _: Resp = try await core("POST", "/connect", ["password": s.password, "tailcat": s.tailcat])
        connected = true
    }

    private func count() async throws {
        phase = .counting
        assetsSeen = 0
        let found = try await MediaLibrary.items(accountID: accountID, store: store) { n in
            Task { @MainActor in self.assetsSeen = n }
        }
        items = found
        totalBytes = found.reduce(0) { $0 + $1.size }
        counted = true
        // Forget state about items no longer selected.
        let keys = Set(found.map(\.id))
        refs = refs.filter { keys.contains($0.key) }
        onServer.formIntersection(keys)
        missing.formIntersection(keys)
        hashedBytes = found.lazy.filter { self.refs[$0.id] != nil }.reduce(0) { $0 + $1.size }
    }

    /// loadCachedHashes gets the core's cached hashes of the items.
    private func loadCachedHashes() async throws {
        struct Resp: Decodable { var refs: [String: [String]] }
        let keys = items.map(\.id).filter { refs[$0] == nil }
        for start in stride(from: 0, to: keys.count, by: 1000) {
            try Task.checkCancellation()
            let r: Resp = try await core("POST", "/hashes/lookup", ["keys": Array(keys[start..<min(start + 1000, keys.count)])])
            for (k, v) in r.refs {
                refs[k] = v
            }
        }
        hashedBytes = items.lazy.filter { self.refs[$0.id] != nil }.reduce(0) { $0 + $1.size }
    }

    private func prepare() async throws {
        if !counted {
            try await count()
        }
        try await connect()
        try await loadCachedHashes()
    }

    private func hashAll() async throws {
        try await prepare()
        phase = .hashing
        let todo = items.filter { refs[$0.id] == nil }
        MediaLibrary.clearTemp()
        try await forEach(todo, limit: Self.maxHashing) { item in
            try await self.skippingFailure(item) {
                let url = try await MediaLibrary.export(item)
                defer { try? FileManager.default.removeItem(at: url) }
                _ = try await self.hash(item, file: url)
            }
        }
    }

    /// skippingFailure runs fn for item, and if it fails other than by
    /// cancellation, counts and reports the failure rather than stopping.
    private func skippingFailure(_ item: MediaItem, _ fn: () async throws -> Void) async throws {
        do {
            try await fn()
        } catch {
            if error is CancellationError || Task.isCancelled {
                throw error
            }
            failed += 1
            notice = "\(failed.formatted()) failed; last, \(item.name): \(error.localizedDescription)"
        }
    }

    /// hash hashes the item's exported file.
    private func hash(_ item: MediaItem, file: URL) async throws -> [String] {
        hashing += 1
        defer { hashing -= 1 }
        struct Resp: Decodable { var refs: [String] }
        let r: Resp = try await core("POST", "/hashes/file", ["key": item.id, "path": file.path])
        refs[item.id] = r.refs
        hashedBytes += item.size
        return r.refs
    }

    private func findAll() async throws {
        try await prepare()
        phase = .finding
        try await find(items.filter { refs[$0.id] != nil && !onServer.contains($0.id) })
    }

    /// find asks the server which of the hashed items it has.
    private func find(_ batch: [MediaItem]) async throws {
        struct Item: Encodable, Sendable { var key: String; var refs: [String] }
        struct Req: Encodable, Sendable { var items: [Item] }
        struct Resp: Decodable { var present: [String]; var missing: [String]; var error: String? }
        for start in stride(from: 0, to: batch.count, by: 500) {
            try Task.checkCancellation()
            let chunk = batch[start..<min(start + 500, batch.count)]
            let r: Resp = try await core("POST", "/findmissing",
                                         Req(items: chunk.compactMap { i in refs[i.id].map { Item(key: i.id, refs: $0) } }))
            onServer.formUnion(r.present)
            missing.subtract(r.present)
            missing.formUnion(r.missing)
            if let e = r.error {
                // Without search, uploading still works: the server's
                // chunks are found as they're uploaded.
                notice = e
                return
            }
        }
    }

    private func uploadAll() async throws {
        try await prepare()
        phase = .finding
        try await find(items.filter { refs[$0.id] != nil && !onServer.contains($0.id) && !missing.contains($0.id) })
        phase = .uploading
        MediaLibrary.clearTemp()
        let poller = Task { await pollUploads() }
        defer { poller.cancel() }
        let todo = items.filter { !onServer.contains($0.id) && !uploaded.contains($0.id) }
        try await forEach(todo, limit: Self.maxUploads) { item in
            try await self.skippingFailure(item) { try await self.uploadOne(item) }
        }
    }

    /// uploadOne exports, hashes (if needed), checks, and uploads one item.
    private func uploadOne(_ item: MediaItem) async throws {
        inFlight.append(InFlight(item: item))
        defer { inFlight.removeAll { $0.id == item.id } }
        Task {
            let img = await MediaLibrary.thumbnail(assetID: item.assetID, size: 56)
            if let i = inFlight.firstIndex(where: { $0.id == item.id }) {
                inFlight[i].thumbnail = img
            }
        }
        let url = try await MediaLibrary.export(item)
        defer { try? FileManager.default.removeItem(at: url) }
        try Task.checkCancellation()
        var itemRefs = refs[item.id]
        if itemRefs == nil {
            setInFlight(item.id, phase: "hashing")
            itemRefs = try await hash(item, file: url)
            try await find([item])
            if onServer.contains(item.id) {
                return
            }
        }
        struct Req: Encodable, Sendable {
            var key, path, name: String
            var modTime: String?
            var refs: [String]
        }
        struct Resp: Decodable { var id: Int }
        let modTime = item.date.map { ISO8601DateFormatter().string(from: $0) }
        setInFlight(item.id, phase: "uploading")
        let r: Resp = try await core("POST", "/uploads", Req(key: item.id, path: url.path, name: item.name, modTime: modTime, refs: itemRefs ?? []))
        if Task.isCancelled {
            // Paused while starting it; the pause's cancel may have missed it.
            pause(notice: notice)
        }
        let done = await waitForUpload(r.id)
        try Task.checkCancellation()
        if let e = done.error {
            throw Core.Error(status: 0, message: e)
        }
        uploaded.insert(item.id)
        missing.remove(item.id)
        uploadedBytes += item.size
    }

    private func setInFlight(_ key: String, phase: String) {
        if let i = inFlight.firstIndex(where: { $0.id == key }) {
            inFlight[i].phase = phase
            inFlight[i].fraction = 0
        }
    }

    struct UploadDone: Decodable {
        var id: Int
        var key: String
        var fileRef: String?
        var dup: Bool
        var error: String?
    }

    /// waitForUpload waits for the core's upload id to finish, as
    /// pollUploads reports.
    private func waitForUpload(_ id: Int) async -> UploadDone {
        if let d = finished.removeValue(forKey: id) {
            return d
        }
        return await withCheckedContinuation { waiters[id] = $0 }
    }

    /// uploadFinished passes a finished upload to its waiter, or keeps it
    /// for waitForUpload, if the poll saw it finish first.
    private func uploadFinished(_ d: UploadDone) {
        if let w = waiters.removeValue(forKey: d.id) {
            w.resume(returning: d)
        } else {
            finished[d.id] = d
        }
    }

    /// pollUploads polls the core for the uploads' progress, a few times a
    /// second, until canceled, then waits for the uploads it started to
    /// finish (as canceled, after a pause).
    private func pollUploads() async {
        struct InFlightResp: Decodable {
            var id: Int
            var key: String
            var size: Int64
            var phase: String
            var BytesHashed: Int64
            var BytesUploaded: Int64
        }
        struct Resp: Decodable {
            var inFlight: [InFlightResp]
            var done: [UploadDone]
            var totals: Totals
        }
        while true {
            let path = "/accounts/\(accountID)/uploads"
            if let r = try? await Task.detached(operation: { try Core.json("GET", path) as Resp }).value {
                totals = r.totals
                noteRate(r.totals.bytesUploaded)
                for f in r.inFlight {
                    if let i = inFlight.firstIndex(where: { $0.id == f.key }) {
                        // Contents are cut into chunks ahead of uploading
                        // them, so show how much is sent.
                        let read = f.phase == "hashing" ? f.BytesHashed : f.BytesUploaded
                        inFlight[i].phase = f.phase
                        inFlight[i].fraction = f.size > 0 ? min(1, Double(read) / Double(f.size)) : 0
                    }
                }
                r.done.forEach(uploadFinished)
            }
            if Task.isCancelled && waiters.isEmpty {
                return
            }
            try? await Task.sleep(for: .milliseconds(250))
        }
    }

    /// noteRate updates the upload rate, averaged over the last few seconds.
    private func noteRate(_ bytes: Int64) {
        let now = Date()
        rateSamples.append((now, bytes))
        rateSamples.removeAll { now.timeIntervalSince($0.0) > 5 }
        if let first = rateSamples.first, now.timeIntervalSince(first.0) > 0.5 {
            bytesPerSecond = Double(bytes - first.1) / now.timeIntervalSince(first.0)
        }
    }

    /// forEach runs fn for each item, at most limit at once, stopping at
    /// the first error or cancellation.
    private func forEach(_ items: [MediaItem], limit: Int, _ fn: @escaping (MediaItem) async throws -> Void) async throws {
        try await withThrowingTaskGroup(of: Void.self) { g in
            var next = 0
            while next < min(limit, items.count) {
                let item = items[next]
                g.addTask { try await fn(item) }
                next += 1
            }
            while try await g.next() != nil {
                try Task.checkCancellation()
                if next < items.count {
                    let item = items[next]
                    g.addTask { try await fn(item) }
                    next += 1
                }
            }
        }
    }
}
