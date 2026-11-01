import Foundation

/// SharedAccounts is a copy of the app's account list, in the keychain,
/// for the share extension, which can't read the app's files.
enum SharedAccounts {
    /// Account is what the share extension needs of an account: the
    /// settings of the core's Account, without its album selection.
    struct Account: Codable, Equatable, Identifiable {
        var id: String
        var name: String
        var server: String
        var tailcat: Bool
        var certfp: String
        var username: String
    }

    struct List: Codable, Equatable {
        var current: String
        var accounts: [Account]
    }

    // Not an account ID, which are only lowercase letters and digits.
    private static let item = "shared-accounts"

    static func load() -> List {
        guard let data = Keychain.data(account: item),
              let l = try? JSONDecoder().decode(List.self, from: data)
        else {
            return List(current: "", accounts: [])
        }
        return l
    }

    /// save saves the list, if it's changed.
    static func save(_ l: List) {
        guard l != load(), let data = try? JSONEncoder().encode(l) else { return }
        try? Keychain.setData(data, account: item)
    }
}
