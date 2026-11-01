import Network
import SwiftUI

/// UploadModel backs up an account's media, keeping stats as it goes. The
/// Go core does the hashing and uploading; see clients/mobile/core/uploads.go.
///
/// The work is a pipeline of four stages, each depending on the one
/// before: counting the files that the account's album rules select,
/// hashing them, asking the server which it has, and uploading the rest.
/// A run goes up to a target stage, with every stage up to it going at
/// once: files are hashed as soon as they're counted, asked about as
/// they're hashed, and uploaded as soon as the server says it lacks them.
///
/// Nothing is done twice. Counting uses a cache of the assets' files (see
/// ResourceCache), and the core caches files' hashes, by a key that
/// changes with their contents, and which hashes its server has a file of,
/// by the server's storage generation. What the server lacks isn't
/// cached, as another client may upload it. While uploading, a file
/// exported from the photo library to hash it is kept for uploading, and
/// the core skips asking the server again about each file it uploads.
/// Chunks the server has are found by the upload itself.
@MainActor
final class UploadModel: ObservableObject {
    /// Stage is how far a run goes.
    enum Stage: Int, Comparable {
        case count, hash, find, upload

        static func < (a: Stage, b: Stage) -> Bool { a.rawValue < b.rawValue }
    }

    enum Phase: String {
        case idle = "Ready"
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

    @Published private(set) var counted = false // a count has finished
    @Published private(set) var countSummary: String?
    @Published private(set) var inFlight: [InFlight] = []

    /// stats is a snapshot of the pipeline's progress, published a few
    /// times a second while running, rather than as the pipeline goes,
    /// which would redraw the screen far too often.
    @Published private(set) var stats = Stats()

    /// Stats are the counts and sizes the upload screen shows.
    struct Stats: Equatable {
        var assetsSeen = 0, assetCount = 0 // while counting, and in all
        var files = 0, totalBytes: Int64 = 0
        var hashed = 0, hashedBytes: Int64 = 0
        var onServer = 0
        var missing = 0, missingBytes: Int64 = 0
        var uploaded = 0, uploadedBytes: Int64 = 0
        var inFlight = 0, inFlightBytes: Int64 = 0
        var toUpload = 0, toUploadBytes: Int64 = 0 // not known to be on the server
        var failed = 0
        var totals = Totals()
        var bytesPerSecond = 0.0
    }

    // The pipeline's state, which stats summarizes.
    private var assetsSeen = 0
    private var assetCount = 0 // photos and videos; items are their files
    private var items: [MediaItem] = []
    private var totalBytes: Int64 = 0
    private var refs: [String: [String]] = [:] // by item key
    private var onServer: Set<String> = []
    private var missing: Set<String> = []
    private var uploaded: Set<String> = []
    private var uploadedBytes: Int64 = 0 // of the items' sizes
    private var failed = 0
    private var totals = Totals()
    private var bytesPerSecond = 0.0

    /// Totals are the core's counts of the connection's uploads.
    struct Totals: Decodable, Equatable {
        var filesDone = 0
        var filesDup = 0
        var filesFailed = 0
        var bytesUploaded: Int64 = 0
        var chunks = 0
        var chunksPresent = 0
        var chunksUploaded = 0
    }

    private var work: Task<Void, Never>?
    private var connection: Task<Void, Error>? // to the server, once started
    private var waiters: [Int: CheckedContinuation<UploadDone, Never>] = [:] // by core upload ID
    private var finished: [Int: UploadDone] = [:] // by core upload ID, with no waiter yet
    private var rateSamples: [(Date, Int64)] = []

    // A run's state.
    private var target = Stage.count
    private var countingDone = false
    private var hashCursor = 0             // into items: those before it are hashed or taken
    private var taken: Set<String> = []    // items being hashed or uploaded
    private var toCheck: [MediaItem] = []  // hashed, not yet asked about
    private var uploadQueue: [MediaItem] = []
    private var hashingNow = 0             // items being hashed
    private var finishing = false          // in finishFinding
    private var findingDone = false        // every counted item is hashed and asked about
    private var searchFailed = false
    private var exported: [String: URL] = [:] // by item key: files kept for uploading
    private var exportedBytes: Int64 = 0

    private let monitor = NWPathMonitor()
    private var onCellular = false
    private var startedOnCellular = false

    private static let maxUploads = 3
    private static let maxHashing = 2
    /// checkBatchSize is how many hashed items are asked about at once.
    private static let checkBatchSize = 50
    /// exportBudget is how many bytes of exported files may be kept for
    /// uploading at once.
    private static let exportBudget: Int64 = 1 << 30

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
        if running && cell && !was && !startedOnCellular && target == .upload {
            pause(notice: "Paused because the phone switched from Wi-Fi to cellular data.")
        }
    }

    // MARK: Stats

    /// updateStats publishes the pipeline's progress, if it's changed.
    private func updateStats() {
        var st = Stats(assetsSeen: assetsSeen, assetCount: assetCount, files: items.count, totalBytes: totalBytes,
                       uploadedBytes: uploadedBytes, failed: failed, totals: totals, bytesPerSecond: bytesPerSecond)
        for item in items {
            let k = item.id
            if refs[k] != nil {
                st.hashed += 1
                st.hashedBytes += item.size
            }
            let there = onServer.contains(k), up = uploaded.contains(k)
            if there {
                st.onServer += 1
            }
            if up {
                st.uploaded += 1
            }
            if missing.contains(k) {
                st.missing += 1
                st.missingBytes += item.size
            }
            if !there && !up {
                st.toUpload += 1
                st.toUploadBytes += item.size
            }
        }
        st.inFlight = inFlight.count
        st.inFlightBytes = inFlight.reduce(0) { $0 + $1.item.size }
        if st != stats {
            stats = st
        }
    }

    // MARK: Actions

    func countLocal() { start(.count, cellularOK: true) }
    func hashLocal() { start(.hash, cellularOK: true) }
    func findMissing() { start(.find, cellularOK: true) }

    /// upload uploads everything missing. On cellular data, it first asks.
    func upload(confirmedCellular: Bool = false) {
        if onCellular && !confirmedCellular {
            confirmCellular = true
            return
        }
        start(.upload, cellularOK: confirmedCellular)
    }

    func pause(notice: String? = nil) {
        work?.cancel()
        self.notice = notice
        Task.detached { _ = try? Core.send("DELETE", "/accounts/\(self.accountID)/uploads") }
    }

    private func start(_ target: Stage, cellularOK: Bool) {
        guard !running else { return }
        running = true
        notice = nil
        startedOnCellular = onCellular && cellularOK
        UIApplication.shared.isIdleTimerDisabled = true
        let refresher = Task {
            while !Task.isCancelled {
                updateStats()
                try? await Task.sleep(for: .milliseconds(250))
            }
        }
        work = Task {
            defer {
                refresher.cancel()
                updateStats()
            }
            do {
                try await run(to: target)
            } catch is CancellationError {
            } catch {
                if !Task.isCancelled {
                    notice = error.localizedDescription
                }
            }
            MediaLibrary.clearTemp()
            exported = [:]
            exportedBytes = 0
            running = false
            phase = .idle
            inFlight = []
            UIApplication.shared.isIdleTimerDisabled = false
        }
    }

    // MARK: The pipeline

    private func run(to target: Stage) async throws {
        self.target = target
        countingDone = false
        hashCursor = 0
        taken = []
        toCheck = []
        uploadQueue = []
        findingDone = false
        searchFailed = false
        failed = 0
        items = []
        totalBytes = 0
        assetsSeen = 0
        phase = .counting
        MediaLibrary.clearTemp()
        if target >= .hash && connection == nil {
            // Connecting needn't wait for counting.
            connection = Task { try await self.connect() }
        }
        let poller = target == .upload ? Task { await pollUploads() } : nil
        defer { poller?.cancel() }
        hashingNow = 0
        let hashWorkers = target >= .hash ? Self.maxHashing : 0
        try await withThrowingTaskGroup(of: Void.self) { g in
            g.addTask { try await self.countAll() }
            for _ in 0..<hashWorkers {
                g.addTask { try await self.hashWorker() }
            }
            if target == .upload {
                for _ in 0..<Self.maxUploads {
                    g.addTask { try await self.uploadWorker() }
                }
            }
            try await g.waitForAll()
        }
    }

    private func core<T: Decodable & Sendable>(_ method: String, _ path: String, _ body: (any Encodable & Sendable)? = nil) async throws -> T {
        let path = "/accounts/\(accountID)" + path
        return try await Task.detached { try Core.json(method, path, body) as T }.value
    }

    private func connect() async throws {
        guard let a = store.account(accountID) else { throw Core.Error(status: 404, message: "No such account.") }
        let s = Keychain.secrets(account: a.id) ?? Secrets()
        struct Resp: Decodable { var hashes: [String] }
        let _: Resp = try await core("POST", "/connect", ["password": s.password, "tailcat": s.tailcat])
    }

    /// connected waits for the connection to the server.
    private func connected() async throws {
        do {
            try await connection?.value
        } catch {
            connection = nil // to try again next run
            throw error
        }
    }

    /// countAll counts the items, passing them on as it goes.
    private func countAll() async throws {
        let start = Date()
        let c = try await MediaLibrary.count(accountID: accountID, store: store,
                                             onStart: { self.assetCount = $0 },
                                             onBatch: { batch, done in try await self.counted(batch, assetsDone: done) })
        assetCount = c.assets
        countSummary = String(format: "Counted in %.1f s, %@ of %@ photos and videos looked up in Photos, the rest cached",
                              Date().timeIntervalSince(start), c.lookedUp.formatted(), c.assets.formatted())
        countingDone = true
        counted = true
        if target > .count {
            phase = target == .hash ? .hashing : target == .find ? .finding : .uploading
        }
    }

    /// counted adds counted items, with their cached hashes, queueing those
    /// already hashed for asking about, and those known missing for
    /// uploading.
    private func counted(_ batch: [MediaItem], assetsDone: Int) async throws {
        assetsSeen = assetsDone
        if target >= .hash {
            try await connected()
            try await loadCachedHashes(batch)
        }
        items += batch
        for item in batch {
            totalBytes += item.size
            guard refs[item.id] != nil else { continue }
            if uploaded.contains(item.id) || onServer.contains(item.id) {
                continue
            }
            if missing.contains(item.id) {
                if target == .upload {
                    uploadQueue.append(item)
                }
            } else if target >= .find {
                toCheck.append(item)
            }
        }
        if toCheck.count >= Self.checkBatchSize {
            try await flushChecks()
        }
    }

    /// loadCachedHashes gets the core's cached hashes of the items.
    private func loadCachedHashes(_ batch: [MediaItem]) async throws {
        struct Resp: Decodable { var refs: [String: [String]] }
        let keys = batch.map(\.id).filter { refs[$0] == nil }
        guard !keys.isEmpty else { return }
        let r: Resp = try await core("POST", "/hashes/lookup", ["keys": keys])
        for (k, v) in r.refs {
            refs[k] = v
        }
    }

    /// hashWorker hashes items until counting is done and there are none
    /// left, passing them on to be asked about.
    private func hashWorker() async throws {
        while true {
            try Task.checkCancellation()
            if let item = nextToHash() {
                try await hashOne(item)
            } else if countingDone {
                try await finishFinding()
                return
            } else {
                try await Task.sleep(for: .milliseconds(50))
            }
        }
    }

    /// hashOne exports and hashes an item, keeping the export when
    /// uploading, and passes it on to be asked about, in a batch.
    private func hashOne(_ item: MediaItem) async throws {
        hashingNow += 1
        defer { hashingNow -= 1 }
        try await skippingFailure(item) {
            let url = try await MediaLibrary.export(item)
            var keep = false
            defer {
                if !keep {
                    try? FileManager.default.removeItem(at: url)
                }
            }
            _ = try await self.hash(item, file: url)
            if target == .upload && exportedBytes + item.size <= Self.exportBudget {
                keep = true
                exported[item.id] = url
                exportedBytes += item.size
            }
        }
        taken.remove(item.id)
        if target >= .find && refs[item.id] != nil {
            toCheck.append(item)
            if toCheck.count >= Self.checkBatchSize {
                try await flushChecks()
            }
        }
    }

    /// finishFinding asks about the last hashed items, once counting is
    /// done and nothing's left to hash or being hashed, and notes that
    /// every item is hashed and asked about.
    private func finishFinding() async throws {
        guard countingDone && hashingNow == 0 && !findingDone && !finishing else { return }
        finishing = true
        defer { finishing = false }
        if target >= .find {
            try await flushChecks()
        }
        if hashingNow == 0 && hashCursor >= items.count {
            findingDone = true
        }
    }

    /// nextToHash takes the next counted item that isn't hashed, or returns
    /// nil if there are none now.
    private func nextToHash() -> MediaItem? {
        while hashCursor < items.count {
            let item = items[hashCursor]
            hashCursor += 1
            if refs[item.id] == nil && !taken.contains(item.id) && !uploaded.contains(item.id) {
                taken.insert(item.id)
                return item
            }
        }
        return nil
    }

    /// hash hashes the item's exported file.
    private func hash(_ item: MediaItem, file: URL) async throws -> [String] {
        struct Resp: Decodable { var refs: [String] }
        let r: Resp = try await core("POST", "/hashes/file", ["key": item.id, "path": file.path])
        refs[item.id] = r.refs
        return r.refs
    }

    private func flushChecks() async throws {
        let batch = toCheck
        toCheck = []
        try await check(batch)
    }

    /// check asks the server about the hashed items, and when uploading,
    /// queues those it lacks: all of them, if it can't say.
    private func check(_ batch: [MediaItem]) async throws {
        guard !batch.isEmpty else { return }
        if !searchFailed, try await !find(batch) {
            searchFailed = true
        }
        for item in batch where !uploaded.contains(item.id) {
            if onServer.contains(item.id) {
                dropExport(item.id)
            } else if target == .upload && (searchFailed || missing.contains(item.id)) {
                uploadQueue.append(item)
            }
        }
    }

    /// find asks the server which of the hashed items it has. It returns
    /// false if the server couldn't say, as without the Search permission.
    private func find(_ batch: [MediaItem]) async throws -> Bool {
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
                return false
            }
        }
        return true
    }

    private func dropExport(_ key: String) {
        guard let url = exported.removeValue(forKey: key) else { return }
        try? FileManager.default.removeItem(at: url)
        if let item = items.first(where: { $0.id == key }) {
            exportedBytes -= item.size
        }
    }

    /// takeExport returns the item's kept exported file, if any, which
    /// the caller then deletes.
    private func takeExport(_ item: MediaItem) -> URL? {
        guard let url = exported.removeValue(forKey: item.id) else { return nil }
        exportedBytes -= item.size
        return url
    }

    /// uploadWorker uploads queued items until every item is hashed and
    /// asked about and the queue's empty, helping hash when there's
    /// nothing to upload yet.
    private func uploadWorker() async throws {
        while true {
            try Task.checkCancellation()
            if let item = nextToUpload() {
                try await skippingFailure(item) { try await self.uploadOne(item) }
                taken.remove(item.id)
            } else if let item = nextToHash() {
                // Nothing to upload yet: help hash.
                try await hashOne(item)
            } else if findingDone {
                return
            } else {
                // The hash workers may have finished before this worker's
                // last hash; then it's up to this one.
                try await finishFinding()
                if !findingDone || !uploadQueue.isEmpty {
                    try await Task.sleep(for: .milliseconds(100))
                }
            }
        }
    }

    private func nextToUpload() -> MediaItem? {
        while !uploadQueue.isEmpty {
            let item = uploadQueue.removeFirst()
            if !uploaded.contains(item.id) && !onServer.contains(item.id) && !taken.contains(item.id) {
                taken.insert(item.id)
                return item
            }
        }
        return nil
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

    /// uploadOne uploads a queued item, exporting it unless the hashing
    /// kept its export.
    private func uploadOne(_ item: MediaItem) async throws {
        inFlight.append(InFlight(item: item))
        defer { inFlight.removeAll { $0.id == item.id } }
        Task {
            let img = await MediaLibrary.thumbnail(assetID: item.assetID, size: 56)
            if let i = inFlight.firstIndex(where: { $0.id == item.id }) {
                inFlight[i].thumbnail = img
            }
        }
        var url = takeExport(item)
        if url == nil {
            url = try await MediaLibrary.export(item)
        }
        guard let url else { return }
        defer { try? FileManager.default.removeItem(at: url) }
        try Task.checkCancellation()
        // Queued items are hashed, and were asked about (or can't be), so
        // the core needn't ask again.
        let itemRefs = refs[item.id] ?? []
        struct Req: Encodable, Sendable {
            var key, path, name: String
            var modTime: String?
            var refs: [String]
            var noDupCheck: Bool
        }
        struct Resp: Decodable { var id: Int }
        let modTime = item.date.map { ISO8601DateFormatter().string(from: $0) }
        setInFlight(item.id, phase: "uploading")
        let r: Resp = try await core("POST", "/uploads", Req(key: item.id, path: url.path, name: item.name,
                                                             modTime: modTime, refs: itemRefs, noDupCheck: true))
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
}
