import Foundation
import Security

/// Secrets are an account's credentials, kept in the keychain.
struct Secrets: Codable, Equatable {
    var password = "" // password or access key
    var tailcat = ""  // tailcat address, which holds a WireGuard pre-shared key
}

/// Keychain stores account secrets, keyed by account ID.
enum Keychain {
    private static let service = "org.perkeep.mobile.account"

    struct Error: LocalizedError {
        let status: OSStatus
        var errorDescription: String? {
            "Keychain error: \(SecCopyErrorMessageString(status, nil) as String? ?? "\(status)")"
        }
    }

    private static func query(_ account: String) -> [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
        ]
    }

    static func secrets(account: String) -> Secrets? {
        var q = query(account)
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var out: CFTypeRef?
        guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess, let data = out as? Data else {
            return nil
        }
        if let s = try? JSONDecoder().decode(Secrets.self, from: data) {
            return s
        }
        // Items saved before tailcat support are just the password.
        return Secrets(password: String(decoding: data, as: UTF8.self))
    }

    static func setSecrets(_ secrets: Secrets, account: String) throws {
        let data = try JSONEncoder().encode(secrets)
        let status = SecItemUpdate(query(account) as CFDictionary, [kSecValueData as String: data] as CFDictionary)
        if status == errSecItemNotFound {
            var q = query(account)
            q[kSecValueData as String] = data
            // Background uploads need the secrets while the phone is locked.
            q[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
            let status = SecItemAdd(q as CFDictionary, nil)
            if status != errSecSuccess {
                throw Error(status: status)
            }
        } else if status != errSecSuccess {
            throw Error(status: status)
        }
    }

    static func deleteSecrets(account: String) {
        SecItemDelete(query(account) as CFDictionary)
    }
}
