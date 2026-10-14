import Foundation
import Security

/// Only Meshway's own single named item is queried. No keychain enumeration.
public enum NativeStateKey {
    public static func loadOrCreate() throws -> Data {
        var query: [CFString: Any] = [
            kSecClass: kSecClassGenericPassword,
            kSecAttrService: "network.meshway.state-vault",
            kSecAttrAccount: "local-state-key-v1",
            kSecAttrSynchronizable: false,
        ]
        if let group = Bundle.main.object(forInfoDictionaryKey: "MeshwayKeychainAccessGroup") as? String,
           !group.contains("$(") {
            query[kSecAttrAccessGroup] = group
            query[kSecUseDataProtectionKeychain] = true
        }
        var lookup = query
        lookup[kSecReturnData] = true; lookup[kSecMatchLimit] = kSecMatchLimitOne
        var result: CFTypeRef?
        let found = SecItemCopyMatching(lookup as CFDictionary, &result)
        if found == errSecSuccess, let data = result as? Data, data.count == 32 { return data }
        guard found == errSecItemNotFound else {
            throw ClientError.invalidConfiguration("Meshway's private storage key is unavailable (Keychain status \(found)). Unlock this device or use the signed app.")
        }
        var bytes = [UInt8](repeating: 0, count: 32)
        guard SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes) == errSecSuccess else {
            throw ClientError.invalidConfiguration("Could not generate Meshway's private storage key.")
        }
        let data = Data(bytes)
        query[kSecValueData] = data
        query[kSecAttrAccessible] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        let saved = SecItemAdd(query as CFDictionary, nil)
        if saved == errSecDuplicateItem { return try loadOrCreate() }
        guard saved == errSecSuccess else {
            throw ClientError.invalidConfiguration("Could not protect Meshway's storage key in Keychain (status \(saved)).")
        }
        return data
    }
}
