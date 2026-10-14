import Foundation
import Darwin

public enum StateMigration {
    /// Copy legacy plaintext only while holding the same lock as the Go core.
    /// Encrypted stores need their original native vault/recovery path.
    public static func copyStoppedLegacyState(from source: URL, to destination: URL) throws {
        let descriptor = open(source.appendingPathComponent("state.lock").path, O_CREAT | O_RDWR, 0o600)
        guard descriptor >= 0 else { throw ClientError.invalidConfiguration("Could not lock the previous Meshway identity store.") }
        defer { close(descriptor) }
        guard flock(descriptor, LOCK_EX | LOCK_NB) == 0 else {
            throw ClientError.invalidConfiguration("Stop the previous Meshway node before migrating its identity into the VPN app group.")
        }
        defer { _ = flock(descriptor, LOCK_UN) }
        for filename in ["identity.key", "account.seed"] {
            let path = source.appendingPathComponent(filename)
            guard FileManager.default.fileExists(atPath: path.path) else { continue }
            let file = try FileHandle(forReadingFrom: path); defer { try? file.close() }
            if try file.read(upToCount: 17)?.starts(with: Data("meshway-vault-v1\0".utf8)) == true {
                throw ClientError.invalidConfiguration("The previous app has an encrypted identity store. Export its recovery file in that app and import it in this signed VPN app.")
            }
        }
        try FileManager.default.copyItem(at: source, to: destination)
        try FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: destination.path)
    }
}
