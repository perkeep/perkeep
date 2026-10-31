import Foundation
import Security

/// Keychain stores account passwords, keyed by account ID.
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

    static func password(account: String) -> String? {
        var q = query(account)
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var out: CFTypeRef?
        guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess, let data = out as? Data else {
            return nil
        }
        return String(data: data, encoding: .utf8)
    }

    static func setPassword(_ password: String, account: String) throws {
        let data = Data(password.utf8)
        let status = SecItemUpdate(query(account) as CFDictionary, [kSecValueData as String: data] as CFDictionary)
        if status == errSecItemNotFound {
            var q = query(account)
            q[kSecValueData as String] = data
            // Background uploads need the password while the phone is locked.
            q[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
            let status = SecItemAdd(q as CFDictionary, nil)
            if status != errSecSuccess {
                throw Error(status: status)
            }
        } else if status != errSecSuccess {
            throw Error(status: status)
        }
    }

    static func deletePassword(account: String) {
        SecItemDelete(query(account) as CFDictionary)
    }
}
