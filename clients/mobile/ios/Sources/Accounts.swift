import Foundation

/// Account is a Perkeep server to back up to. See Account in
/// clients/mobile/core/accounts.go.
struct Account: Codable, Identifiable, Hashable {
    var id: String
    var name: String
    var server: String
    var username: String
    var include: [Album]
    var exclude: [Album]
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

/// AccountStore is the app's view of the accounts kept by the core.
@MainActor
final class AccountStore: ObservableObject {
    struct List: Decodable {
        var current: String
        var accounts: [Account]
    }

    @Published private(set) var list = List(current: "", accounts: [])
    @Published var lastError: String?

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

    private struct Fields: Encodable {
        var name: String
        var server: String
        var username: String
    }

    /// add adds an account and returns it. If password is non-empty, it's
    /// stored in the keychain.
    func add(name: String, server: String, username: String, password: String) throws -> Account {
        let a: Account = try Core.json("POST", "/accounts", Fields(name: name, server: server, username: username))
        if !password.isEmpty {
            try Keychain.setPassword(password, account: a.id)
        }
        reload()
        return a
    }

    /// update changes an account's settings. If password is non-nil, it
    /// replaces the stored password.
    func update(_ id: String, name: String, server: String, username: String, password: String?) throws {
        try Core.send("PUT", "/accounts/\(id)", Fields(name: name, server: server, username: username))
        if let password {
            try Keychain.setPassword(password, account: id)
        }
        reload()
    }

    func delete(_ id: String) {
        do {
            try Core.send("DELETE", "/accounts/\(id)")
            Keychain.deletePassword(account: id)
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
