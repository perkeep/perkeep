import SwiftUI
import UniformTypeIdentifiers

/// ShareViewController is the share extension's entry point: it uploads
/// the photos and videos shared from another app, such as Photos, to a
/// Perkeep account. With one account, it starts uploading at once; with
/// more, it asks which.
///
/// The extension can't read the app's files, so it has its own Go core
/// and finds the app's accounts in the keychain (see SharedAccounts).
@objc(ShareViewController)
final class ShareViewController: UIViewController {
    override func viewDidLoad() {
        super.viewDidLoad()
        guard let ctx = extensionContext else { return }
        let model = ShareModel(context: ctx)
        let host = UIHostingController(rootView: ShareView(model: model))
        addChild(host)
        host.view.frame = view.bounds
        host.view.autoresizingMask = [.flexibleWidth, .flexibleHeight]
        view.addSubview(host.view)
        host.didMove(toParent: self)
        model.start()
    }
}

@MainActor
final class ShareModel: ObservableObject {
    enum State {
        case loading
        case noAccounts
        case choose([SharedAccounts.Account])
        case uploading(String)   // to the named account
        case done(String, Int)   // to the named account, how many
        case failed(String)
    }

    @Published private(set) var state = State.loading
    @Published private(set) var filesTotal = 0
    @Published private(set) var filesDone = 0
    @Published private(set) var bytesSent: Int64 = 0
    @Published private(set) var errors: [String] = []

    private let context: NSExtensionContext
    private var work: Task<Void, Never>?

    init(context: NSExtensionContext) {
        self.context = context
    }

    func start() {
        Core.initialize()
        let l = SharedAccounts.load()
        switch l.accounts.count {
        case 0:
            state = .noAccounts
        case 1:
            upload(to: l.accounts[0])
        default:
            // The app's current account first.
            state = .choose(l.accounts.sorted { a, _ in a.id == l.current })
        }
    }

    func cancel() {
        work?.cancel()
        let path = "/accounts/\(accountID)/uploads"
        Task.detached { _ = try? Core.send("DELETE", path) }
        context.cancelRequest(withError: CocoaError(.userCancelled))
    }

    func close() {
        context.completeRequest(returningItems: nil)
    }

    private var accountID = ""

    func upload(to a: SharedAccounts.Account) {
        accountID = a.id
        state = .uploading(a.name)
        work = Task {
            do {
                let n = try await doUpload(to: a)
                state = .done(a.name, n)
                if errors.isEmpty {
                    try? await Task.sleep(for: .seconds(1))
                    close()
                }
            } catch is CancellationError {
            } catch {
                state = .failed(error.localizedDescription)
            }
        }
    }

    private func core<T: Decodable & Sendable>(_ method: String, _ path: String, _ body: (any Encodable & Sendable)? = nil) async throws -> T {
        try await Task.detached { try Core.json(method, path, body) as T }.value
    }

    /// doUpload uploads the shared files to the account, returning how
    /// many it uploaded.
    private func doUpload(to a: SharedAccounts.Account) async throws -> Int {
        // This process's core has its own copy of the account.
        struct Fields: Encodable, Sendable {
            var id, name, server: String
            var tailcat: Bool
            var certfp, username: String
        }
        let f = Fields(id: a.id, name: a.name, server: a.server, tailcat: a.tailcat, certfp: a.certfp, username: a.username)
        struct List: Decodable { var accounts: [Account] }
        let l: List = try await core("GET", "/accounts")
        if l.accounts.contains(where: { $0.id == a.id }) {
            let _: Account = try await core("PUT", "/accounts/\(a.id)", f)
        } else {
            let _: Account = try await core("POST", "/accounts", f)
        }
        let s = Keychain.secrets(account: a.id) ?? Secrets()
        struct Connected: Decodable { var hashes: [String] }
        let _: Connected = try await core("POST", "/accounts/\(a.id)/connect", ["password": s.password, "tailcat": s.tailcat])

        let files = try await sharedFiles()
        defer { files.forEach { try? FileManager.default.removeItem(at: $0) } }
        filesTotal = files.count
        guard !files.isEmpty else {
            throw Core.Error(status: 0, message: "There are no photos or videos to upload.")
        }
        struct Start: Encodable, Sendable { var key, path, name: String }
        struct Started: Decodable { var id: Int }
        var ids = Set<Int>()
        for (i, url) in files.enumerated() {
            let r: Started = try await core("POST", "/accounts/\(a.id)/uploads",
                                            Start(key: "share-\(i)", path: url.path, name: Self.uploadName(url)))
            ids.insert(r.id)
        }
        struct Done: Decodable { var id: Int; var error: String? }
        struct Totals: Decodable { var bytesUploaded: Int64 }
        struct Status: Decodable { var done: [Done]; var totals: Totals }
        while !ids.isEmpty {
            try await Task.sleep(for: .milliseconds(250))
            let st: Status = try await core("GET", "/accounts/\(a.id)/uploads")
            bytesSent = st.totals.bytesUploaded
            for d in st.done where ids.remove(d.id) != nil {
                filesDone += 1
                if let e = d.error {
                    errors.append(e)
                }
            }
        }
        return filesDone - errors.count
    }

    /// uploadName is the name to upload a shared file as: its own,
    /// without the unique directory it was copied into.
    private static func uploadName(_ url: URL) -> String { url.lastPathComponent }

    /// sharedFiles copies the shared photos and videos to temporary files,
    /// each in its own directory, to keep its name.
    private func sharedFiles() async throws -> [URL] {
        var out: [URL] = []
        let items = context.inputItems.compactMap { $0 as? NSExtensionItem }
        for p in items.flatMap({ $0.attachments ?? [] }) {
            guard let type = p.registeredTypeIdentifiers.first(where: {
                guard let t = UTType($0) else { return false }
                return t.conforms(to: .image) || t.conforms(to: .movie)
            }) else { continue }
            out.append(try await Self.copy(p, type: type))
        }
        return out
    }

    private static func copy(_ p: NSItemProvider, type: String) async throws -> URL {
        try await withCheckedThrowingContinuation { c in
            _ = p.loadFileRepresentation(forTypeIdentifier: type) { url, err in
                guard let url else {
                    c.resume(throwing: err ?? CocoaError(.fileReadUnknown))
                    return
                }
                // url is deleted when this returns.
                do {
                    let dir = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
                    try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
                    let dst = dir.appendingPathComponent(url.lastPathComponent)
                    try FileManager.default.copyItem(at: url, to: dst)
                    c.resume(returning: dst)
                } catch {
                    c.resume(throwing: error)
                }
            }
        }
    }
}

struct ShareView: View {
    @ObservedObject var model: ShareModel

    var body: some View {
        NavigationStack {
            content
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .navigationTitle("Perkeep")
                .navigationBarTitleDisplayMode(.inline)
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) {
                        Button("Cancel") { model.cancel() }
                    }
                    if case .done = model.state {
                        ToolbarItem(placement: .confirmationAction) {
                            Button("Done") { model.close() }
                        }
                    }
                }
        }
    }

    @ViewBuilder private var content: some View {
        switch model.state {
        case .loading:
            ProgressView()
        case .noAccounts:
            ContentUnavailableView("No Perkeep Server", systemImage: "server.rack",
                                   description: Text("Add a server in the Perkeep app first."))
        case .choose(let accounts):
            List(accounts) { a in
                Button {
                    model.upload(to: a)
                } label: {
                    VStack(alignment: .leading) {
                        Text(a.name)
                        Text(a.tailcat ? "via tailcat" : a.server).font(.caption).foregroundStyle(.secondary)
                    }
                }
            }
            .listStyle(.insetGrouped)
            .safeAreaInset(edge: .top) {
                Text("Upload to which server?").font(.headline)
            }
        case .uploading(let name):
            VStack(spacing: 12) {
                ProgressView(value: Double(model.filesDone), total: Double(max(model.filesTotal, 1)))
                Text("Uploading to \(name)…").font(.headline)
                Text("\(model.filesDone) of \(model.filesTotal) · "
                     + ByteCountFormatter.string(fromByteCount: model.bytesSent, countStyle: .file) + " sent")
                    .font(.subheadline.monospacedDigit())
                    .foregroundStyle(.secondary)
            }
            .padding()
        case .done(let name, let n):
            VStack(spacing: 12) {
                Image(systemName: model.errors.isEmpty ? "checkmark.icloud.fill" : "exclamationmark.icloud")
                    .font(.system(size: 48))
                    .foregroundStyle(model.errors.isEmpty ? .green : .orange)
                Text("Uploaded \(n) to \(name)").font(.headline)
                ForEach(model.errors, id: \.self) { e in
                    Text(e).font(.caption).foregroundStyle(.secondary)
                }
            }
            .padding()
        case .failed(let msg):
            ContentUnavailableView("Couldn't Upload", systemImage: "exclamationmark.icloud", description: Text(msg))
        }
    }
}
