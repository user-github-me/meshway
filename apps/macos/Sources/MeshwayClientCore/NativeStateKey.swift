import Foundation
import Security

/// Only Meshway's own single named item is queried. No keychain enumeration.
public enum NativeStateKey {
    private static let interactionLock = NSLock()
    public static func hasEntitlement(_ name: String, containing expected: String) -> Bool {
        #if os(macOS)
        guard let task = SecTaskCreateFromSelf(nil),
              let values = SecTaskCopyValueForEntitlement(task, name as CFString, nil) as? [String] else { return false }
        return values.contains(expected)
        #else
        // iOS does not expose SecTask. The container API validates the app's
        // signed app-group entitlement before returning its shared directory.
        guard name == "com.apple.security.application-groups" else { return false }
        return FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: expected) != nil
        #endif
    }

    public static func loadOrCreate(allowInteraction: Bool = true) throws -> Data {
        interactionLock.lock(); defer { interactionLock.unlock() }
        #if os(macOS)
        var previousInteraction: DarwinBoolean = true
        let changedInteraction = !allowInteraction && SecKeychainGetUserInteractionAllowed(&previousInteraction) == errSecSuccess && SecKeychainSetUserInteractionAllowed(false) == errSecSuccess
        defer { if changedInteraction { _ = SecKeychainSetUserInteractionAllowed(previousInteraction.boolValue) } }
        #endif
        var query: [CFString: Any] = [
            kSecClass: kSecClassGenericPassword,
            kSecAttrService: "network.meshway.state-vault",
            kSecAttrAccount: "local-state-key-v1",
            kSecAttrSynchronizable: false,
        ]
        if let group = Bundle.main.object(forInfoDictionaryKey: "MeshwayKeychainAccessGroup") as? String,
           !group.isEmpty, !group.contains("$(") {
            #if os(macOS)
            if hasEntitlement("keychain-access-groups", containing: group) {
                query[kSecAttrAccessGroup] = group
                query[kSecUseDataProtectionKeychain] = true
            }
            #else
            // Security validates this access group against the signed iOS
            // entitlements. Missing authorization must fail rather than create
            // separate app/extension keys for the same shared node state.
            query[kSecAttrAccessGroup] = group
            #endif
        }
        var lookup = query
        if !allowInteraction { lookup[kSecUseAuthenticationUI] = kSecUseAuthenticationUIFail }
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
        if saved == errSecDuplicateItem {
            var existing: CFTypeRef?
            let status = SecItemCopyMatching(lookup as CFDictionary, &existing)
            if status == errSecSuccess, let data = existing as? Data, data.count == 32 { return data }
            throw ClientError.invalidConfiguration("Meshway’s protected storage key could not be reopened (Keychain status \(status)).")
        }
        guard saved == errSecSuccess else {
            throw ClientError.invalidConfiguration("Could not protect Meshway's storage key in Keychain (status \(saved)).")
        }
        return data
    }
}
