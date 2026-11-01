import Foundation

/// Account is a Perkeep server to back up to. See Account in
/// clients/mobile/core/accounts.go.
struct Account: Codable, Identifiable, Hashable {
    var id: String
    var name: String
    var server: String  // base URL; empty if tailcat
    var tailcat: Bool   // connect through the server's tailcat listener
    var certfp: String  // pinned certificate SHA-256, or empty
    var username: String
    var include: [Album]
    var exclude: [Album]

    /// location describes where the server is, for showing.
    var location: String { tailcat ? "via tailcat" : server }
}

/// AccessInfo is a parsed perkeep://access URL from a server's Client
/// access page. See doc/access-url.md.
struct AccessInfo: Codable, Hashable {
    var name: String
    var key: String
    var tailcat: String
    var url: String
    var certfp: String

    /// parse parses an access URL with the core.
    static func parse(_ url: String) throws -> AccessInfo {
        try Core.json("POST", "/parseaccessurl", ["url": url])
    }
}

/// ServerFields are an account's settings other than albums, as edited in
/// the account form.
struct ServerFields: Equatable {
    var name = ""
    var server = ""
    var tailcat = "" // tailcat address; if set, server and certfp are unused
    var certfp = ""
    var username = ""
    var password = ""

    init() {}

    init(_ ai: AccessInfo) {
        name = ai.name
        server = ai.url
        tailcat = ai.tailcat
        certfp = ai.certfp
        password = ai.key
    }

    var secrets: Secrets { Secrets(password: password, tailcat: tailcat) }

    fileprivate struct Body: Encodable {
        var name, server: String
        var tailcat: Bool
        var certfp, username: String
    }

    fileprivate var body: Body {
        let tc = !tailcat.isEmpty
        return Body(name: name, server: tc ? "" : server, tailcat: tc, certfp: tc ? "" : certfp, username: username)
    }

    /// conn is how the core should reach the server.
    var conn: Conn {
        Conn(server: tailcat.isEmpty ? server : "", certfp: tailcat.isEmpty ? certfp : "",
             tailcat: tailcat, username: username, password: password)
    }
}

/// Conn is how to reach a server; see conn in clients/mobile/core/check.go.
struct Conn: Encodable {
    var server, certfp, tailcat, username, password: String
    var timeout: Double? // seconds, for checks
}

/// CheckResult is the result of checking a server's credentials, with
/// what its discovery says about them. See doc/protocol/discovery.md.
struct CheckResult: Decodable {
    var ok: Bool
    var status: Int
    var message: String
    var ownerName: String?
    var seconds: Double
    var client: Client?
    var storage: Storage?

    struct Client: Decodable {
        var serverName: String?
        var accessKeyName: String?
        var permissions: [String]?
    }

    struct Storage: Decodable {
        var approxBlobs: Int64?
        var approxBytes: Int64?
    }

    /// durationText is how long the check took, like "45 ms" or "1.2 s".
    var durationText: String {
        seconds < 1 ? "\(Int((seconds * 1000).rounded())) ms" : String(format: "%.1f s", seconds)
    }
}

/// Permission is a permission an access key can have.
enum Permission: String, CaseIterable, Identifiable {
    case upload, sign, search, download, stats

    var id: String { rawValue }

    var title: String {
        switch self {
        case .upload: "Upload"
        case .sign: "Make claims"
        case .search: "Search"
        case .download: "Download"
        case .stats: "Server stats"
        }
    }

    var symbol: String {
        switch self {
        case .upload: "icloud.and.arrow.up"
        case .sign: "signature"
        case .search: "magnifyingglass"
        case .download: "icloud.and.arrow.down"
        case .stats: "chart.bar"
        }
    }
}

/// AlbumStates is the core's report of which of the device's albums an
/// account backs up.
struct AlbumStates: Decodable {
    struct Entry: Decodable {
        var album: Album
        var state: AlbumState
    }
    struct Unmatched: Decodable {
        var include: [Album]
        var exclude: [Album]
    }
    var albums: [Entry]
    var unmatched: Unmatched
}

/// Album is a photo album on the device, or an account's reference to one.
struct Album: Codable, Hashable {
    var id: String
    var title: String
    var cameraRoll: Bool
}

enum AlbumState: String, Codable, CaseIterable {
    case include, exclude, none
}

/// AccountStore is the app's view of the accounts kept by the core.
@MainActor
final class AccountStore: ObservableObject {
    struct List: Decodable {
        var current: String
        var accounts: [Account]
    }

    @Published private(set) var list = List(current: "", accounts: [])
    @Published var lastError: String?

    init() {
        reload()
    }

    var current: Account? { account(list.current) }

    func account(_ id: String) -> Account? {
        list.accounts.first { $0.id == id }
    }

    func reload() {
        do {
            list = try Core.json("GET", "/accounts")
        } catch {
            lastError = error.localizedDescription
        }
    }

    /// fields returns the form fields for an existing account.
    func fields(_ a: Account) -> ServerFields {
        var f = ServerFields()
        let s = Keychain.secrets(account: a.id) ?? Secrets()
        f.name = a.name
        f.server = a.server
        f.certfp = a.certfp
        f.username = a.username
        f.password = s.password
        f.tailcat = s.tailcat
        return f
    }

    /// existing returns the account for the same server as f, if any.
    /// Access URLs say to match on the tailcat address or URL, not the name,
    /// which the server's owner can change.
    func existing(matching f: ServerFields) -> Account? {
        list.accounts.first { a in
            if !f.tailcat.isEmpty {
                return a.tailcat && Keychain.secrets(account: a.id)?.tailcat == f.tailcat
            }
            return !a.tailcat && a.server.trimmingSuffix("/") == f.server.trimmingSuffix("/")
        }
    }

    /// add adds an account with f's settings and returns it.
    func add(_ f: ServerFields) throws -> Account {
        let a: Account = try Core.json("POST", "/accounts", f.body)
        try Keychain.setSecrets(f.secrets, account: a.id)
        reload()
        return a
    }

    /// update changes an account's settings to f's.
    func update(_ id: String, _ f: ServerFields) throws {
        try Core.send("PUT", "/accounts/\(id)", f.body)
        try Keychain.setSecrets(f.secrets, account: id)
        reload()
    }

    func delete(_ id: String) {
        do {
            try Core.send("DELETE", "/accounts/\(id)")
            Keychain.deleteSecrets(account: id)
        } catch {
            lastError = error.localizedDescription
        }
        reload()
    }

    func select(_ id: String) {
        do {
            try Core.send("POST", "/accounts/\(id)/select")
        } catch {
            lastError = error.localizedDescription
        }
        reload()
    }

    /// check checks that the server in f is reachable and accepts its
    /// credentials, waiting at most timeout seconds, if given, or else
    /// the core's default. It can take a while, especially over tailcat.
    nonisolated func check(_ f: ServerFields, timeout: Double? = nil) async throws -> CheckResult {
        var conn = f.conn
        conn.timeout = timeout
        return try await Task.detached {
            try Core.json("POST", "/check", conn) as CheckResult
        }.value
    }

    func albumStates(_ id: String, albums: [Album]) throws -> AlbumStates {
        try Core.json("POST", "/accounts/\(id)/albumstates", ["albums": albums])
    }

    func setAlbum(_ id: String, _ album: Album, _ state: AlbumState) throws {
        struct Req: Encodable {
            var album: Album
            var state: AlbumState
        }
        try Core.send("PUT", "/accounts/\(id)/albums", Req(album: album, state: state))
        reload()
    }
}

extension Account {
    /// albumSummary describes the album selection, like
    /// "Camera Roll, except WhatsApp".
    var albumSummary: String {
        let inc = include.map(\.title).joined(separator: ", ")
        let exc = exclude.map(\.title).joined(separator: ", ")
        switch (inc.isEmpty, exc.isEmpty) {
        case (true, _): return "Nothing"
        case (false, true): return inc
        case (false, false): return "\(inc), except \(exc)"
        }
    }
}

extension String {
    func trimmingSuffix(_ suffix: String) -> String {
        hasSuffix(suffix) ? String(dropLast(suffix.count)) : self
    }
}
