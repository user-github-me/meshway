import Foundation
import XCTest
@testable import MeshwayClientCore

private final class FakeProxy {
    struct Value: Equatable { var enabled: Bool; var host: String; var port: Int; var authenticated = false }
    var services = ["Wi-Fi": Value(enabled: false, host: "", port: 0), "Ethernet": Value(enabled: true, host: "old.example", port: 1088)]
    var failOn: String?
    var writes = 0
    var keys: [String: SystemProxyController.ProxyKeys]?
    var failRestoreState = false
    func run(_ args: [String], _ privileged: Bool) throws -> String {
        if args[0] == "-listallnetworkservices" { return "An asterisk denotes a disabled service.\n" + services.keys.sorted().joined(separator: "\n") + "\n*Disabled service\n" }
        if args[0] == "-applymeshwayproxy" {
            XCTAssertTrue(privileged); writes += 1
            let updates = try JSONDecoder().decode([SystemProxyController.Update].self, from: Data(base64Encoded: args[1])!)
            for update in updates {
                if keys != nil { keys![update.service] = update.keys }
                guard var value = services[update.service] else { throw ClientError.invalidConfiguration("missing") }
                value.host = update.keys.host ?? ""; value.port = update.keys.port ?? 0; value.enabled = false
                services[update.service] = value
                if failOn == update.service, update.keys.enabled == 1 { failOn = nil; throw ClientError.invalidConfiguration("denied") }
                if failRestoreState, update.keys.enabled == 1, value.host == "old.example" { failRestoreState = false; throw ClientError.invalidConfiguration("restore denied") }
                value.enabled = update.keys.enabled == 1; services[update.service] = value
            }
            return ""
        }
        let name = args[1]; guard var value = services[name] else { throw ClientError.invalidConfiguration("missing") }
        if args[0] == "-getsocksfirewallproxy" {
            XCTAssertFalse(privileged)
            return "Enabled: \(value.enabled ? "Yes" : "No")\nServer: \(value.host)\nPort: \(value.port)\nAuthenticated Proxy Enabled: \(value.authenticated ? 1 : 0)\n"
        }
        XCTAssertTrue(privileged); writes += 1
        if failRestoreState, args[0] == "-setsocksfirewallproxystate", args[2] == "on", value.host == "old.example" { failRestoreState = false; throw ClientError.invalidConfiguration("restore denied") }
        if failOn == name, args[0] == "-setsocksfirewallproxystate", args[2] == "on" { failOn = nil; throw ClientError.invalidConfiguration("denied") }
        if args[0] == "-setsocksfirewallproxy" { value.host = args[2]; value.port = Int(args[3])! }
        else { value.enabled = args[2] == "on" }
        services[name] = value; return ""
    }
}

final class SystemProxyTests: XCTestCase {
    private func directory() throws -> URL {
        let path = FileManager.default.temporaryDirectory.appendingPathComponent("meshway-proxy-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: path, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        return path
    }
    func testRestoreOriginalValuesAndPrivateJournal() throws {
        let dir = try directory(); defer { try? FileManager.default.removeItem(at: dir) }
        let fake = FakeProxy(); let original = fake.services
        let controller = SystemProxyController(directory: dir, runner: fake.run)
        try controller.enable(host: "127.0.0.1", port: 1080)
        XCTAssertTrue(controller.enabled)
        XCTAssertTrue(fake.services.values.allSatisfy { $0.enabled && $0.host == "127.0.0.1" && $0.port == 1080 })
        let journal = dir.appendingPathComponent("system-proxy-restore.json")
        XCTAssertEqual((try FileManager.default.attributesOfItem(atPath: journal.path)[.posixPermissions] as? NSNumber)?.intValue, 0o600)
        try controller.restore(); XCTAssertEqual(fake.services, original); XCTAssertFalse(FileManager.default.fileExists(atPath: journal.path))
    }
    func testPartialActivationFailureRollsBackEveryChangedService() throws {
        let dir = try directory(); defer { try? FileManager.default.removeItem(at: dir) }
        let fake = FakeProxy(); let original = fake.services; fake.failOn = "Wi-Fi"
        let controller = SystemProxyController(directory: dir, runner: fake.run)
        XCTAssertThrowsError(try controller.enable(host: "127.0.0.1", port: 1080))
        XCTAssertEqual(fake.services, original); XCTAssertFalse(controller.enabled)
    }
    func testAuthenticatedExistingProxyIsNeverChanged() throws {
        let dir = try directory(); defer { try? FileManager.default.removeItem(at: dir) }
        let fake = FakeProxy(); fake.services["Wi-Fi"]!.authenticated = true
        let controller = SystemProxyController(directory: dir, runner: fake.run)
        XCTAssertThrowsError(try controller.enable(host: "127.0.0.1", port: 1080)); XCTAssertEqual(fake.writes, 0)
    }
    func testCrashRecoveryAndLaterUserChangeArePreserved() throws {
        let dir = try directory(); defer { try? FileManager.default.removeItem(at: dir) }
        let fake = FakeProxy(); let original = fake.services
        var first: SystemProxyController? = SystemProxyController(directory: dir, runner: fake.run)
        try first!.enable(host: "127.0.0.1", port: 1080); first = nil
        fake.services["Wi-Fi"] = .init(enabled: true, host: "user.example", port: 9000)
        let recovered = SystemProxyController(directory: dir, runner: fake.run)
        XCTAssertTrue(recovered.needsRecovery); XCTAssertFalse(recovered.enabled)
        try recovered.restore()
        XCTAssertEqual(fake.services["Ethernet"], original["Ethernet"])
        XCTAssertEqual(fake.services["Wi-Fi"]!.host, "user.example")
        XCTAssertFalse(recovered.needsRecovery)
    }
    func testInterruptedFinalStateRestoreRetainsJournalAndCanRetry() throws {
        let dir = try directory(); defer { try? FileManager.default.removeItem(at: dir) }
        let fake = FakeProxy(); let original = fake.services
        let controller = SystemProxyController(directory: dir, runner: fake.run)
        try controller.enable(host: "127.0.0.1", port: 1080)
        fake.failRestoreState = true
        XCTAssertThrowsError(try controller.restore())
        XCTAssertTrue(controller.needsRecovery)
        XCTAssertEqual(fake.services["Ethernet"]!.host, "old.example")
        XCTAssertFalse(fake.services["Ethernet"]!.enabled)
        try controller.restore()
        XCTAssertEqual(fake.services, original); XCTAssertFalse(controller.needsRecovery)
    }
    func testOneBatchDisablesPriorityProxiesAndRestoresAbsentKeys() throws {
        let dir = try directory(); defer { try? FileManager.default.removeItem(at: dir) }
        let fake = FakeProxy()
        fake.keys = ["Wi-Fi": .init(host: nil, port: nil, enabled: nil, httpEnabled: 1, httpsEnabled: 1, pacEnabled: 1), "Ethernet": .init(host: "old.example", port: 1088, enabled: 1)]
        let original = fake.keys!
        let controller = SystemProxyController(directory: dir, runner: fake.run, snapshotter: { fake.keys![$0]! })
        try controller.enable(host: "127.0.0.1", port: 1080)
        XCTAssertEqual(fake.writes, 1)
        XCTAssertTrue(fake.keys!.values.allSatisfy { $0.httpEnabled == 0 && $0.httpsEnabled == 0 && $0.pacEnabled == 0 })
        try controller.restore()
        XCTAssertEqual(fake.writes, 2); XCTAssertEqual(fake.keys!, original)
    }
    func testLaterHTTPChangeDoesNotLeaveOwnedSOCKSBehind() throws {
        let dir = try directory(); defer { try? FileManager.default.removeItem(at: dir) }
        let fake = FakeProxy()
        fake.keys = ["Wi-Fi": .init(host: nil, port: nil, enabled: nil), "Ethernet": .init(host: "old.example", port: 1088, enabled: 1)]
        let controller = SystemProxyController(directory: dir, runner: fake.run, snapshotter: { fake.keys![$0]! })
        try controller.enable(host: "127.0.0.1", port: 1080)
        fake.keys!["Wi-Fi"]!.httpEnabled = 1
        try controller.restore()
        XCTAssertEqual(fake.keys!["Wi-Fi"]!.httpEnabled, 1)
        XCTAssertNil(fake.keys!["Wi-Fi"]!.host); XCTAssertNil(fake.keys!["Wi-Fi"]!.port); XCTAssertNil(fake.keys!["Wi-Fi"]!.enabled)
    }
    func testLegacyJournalRestoresSOCKSWithoutChangingPriorityFlags() throws {
        let dir = try directory(); defer { try? FileManager.default.removeItem(at: dir) }
        let fake = FakeProxy()
        fake.keys = ["Wi-Fi": .init(host: nil, port: nil, enabled: nil, httpEnabled: 1), "Ethernet": .init(host: "old.example", port: 1088, enabled: 1)]
        let original = fake.keys!
        var first: SystemProxyController? = SystemProxyController(directory: dir, runner: fake.run, snapshotter: { fake.keys![$0]! })
        try first!.enable(host: "127.0.0.1", port: 1080); first = nil
        let path = dir.appendingPathComponent("system-proxy-restore.json")
        var saved = try JSONSerialization.jsonObject(with: Data(contentsOf: path)) as! [String: Any]
        saved["version"] = 1; saved.removeValue(forKey: "restoring"); try JSONSerialization.data(withJSONObject: saved).write(to: path)
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: path.path)
        fake.keys!["Wi-Fi"]!.httpEnabled = 1
        let recovered = SystemProxyController(directory: dir, runner: fake.run, snapshotter: { fake.keys![$0]! })
        try recovered.restore()
        XCTAssertNil(fake.keys!["Wi-Fi"]!.host); XCTAssertEqual(fake.keys!["Wi-Fi"]!.httpEnabled, 1)
        XCTAssertEqual(fake.keys!["Ethernet"]!.host, original["Ethernet"]!.host)
    }
    func testProxyDriftInvalidatesWithoutReapplying() async throws {
        let dir = try directory(); defer { try? FileManager.default.removeItem(at: dir) }
        let fake = FakeProxy()
        let controller = SystemProxyController(directory: dir, runner: fake.run, effectiveChecker: { _, _ in true })
        try controller.enable(host: "127.0.0.1", port: 1080)
        let healthy = await controller.verifyStillAppliedAsync(); XCTAssertTrue(healthy)
        let writesBefore = fake.writes
        fake.services["Wi-Fi"]!.enabled = false
        let changed = await controller.verifyStillAppliedAsync(); XCTAssertFalse(changed)
        XCTAssertEqual(fake.writes, writesBefore)
        await MainActor.run { XCTAssertFalse(controller.enabled); XCTAssertTrue(controller.changedExternally); XCTAssertTrue(controller.needsRecovery) }
    }
    func testPriorityHTTPOrPACChangeInvalidatesWithoutOverwritingIt() async throws {
        for flag in ["http", "pac"] {
            let dir = try directory(); defer { try? FileManager.default.removeItem(at: dir) }
            let fake = FakeProxy()
            fake.keys = ["Wi-Fi": .init(host: nil, port: nil, enabled: nil), "Ethernet": .init(host: "old.example", port: 1088, enabled: 1)]
            let controller = SystemProxyController(directory: dir, runner: fake.run, snapshotter: { fake.keys![$0]! }, effectiveChecker: { _, _ in true })
            try controller.enable(host: "127.0.0.1", port: 1080)
            let writesBefore = fake.writes
            if flag == "http" { fake.keys!["Wi-Fi"]!.httpEnabled = 1 } else { fake.keys!["Wi-Fi"]!.pacEnabled = 1 }
            let result = await controller.verifyStillAppliedAsync(); XCTAssertFalse(result)
            XCTAssertEqual(fake.writes, writesBefore)
        }
    }
    func testEffectiveProxyMismatchDoesNotRewritePreferences() async throws {
        let dir = try directory(); defer { try? FileManager.default.removeItem(at: dir) }
        let fake = FakeProxy()
        let controller = SystemProxyController(directory: dir, runner: fake.run, effectiveChecker: { _, _ in false })
        try controller.enable(host: "127.0.0.1", port: 1080)
        let before = fake.writes
        let result = await controller.verifyStillAppliedAsync(); XCTAssertFalse(result)
        XCTAssertEqual(fake.writes, before)
    }
    func testExternalDisableDoesNotRevivePreviousHTTPProxy() throws {
        let dir = try directory(); defer { try? FileManager.default.removeItem(at: dir) }
        let fake = FakeProxy()
        fake.keys = ["Wi-Fi": .init(host: "127.0.0.1", port: 7890, enabled: 1, httpEnabled: 1, httpsEnabled: 1), "Ethernet": .init(host: "old.example", port: 1088, enabled: 1)]
        let controller = SystemProxyController(directory: dir, runner: fake.run, snapshotter: { fake.keys![$0]! })
        try controller.enable(host: "127.0.0.1", port: 1080)
        for name in fake.services.keys { fake.services[name]!.enabled = false; fake.keys![name]!.enabled = 0 }
        try controller.restore()
        XCTAssertEqual(fake.keys!["Wi-Fi"]!.httpEnabled, 0); XCTAssertEqual(fake.keys!["Wi-Fi"]!.httpsEnabled, 0)
        XCTAssertEqual(fake.keys!["Wi-Fi"]!.enabled, 0)
    }
    func testSecondAppCannotOwnNetworkChanges() throws {
        let dir = try directory(); defer { try? FileManager.default.removeItem(at: dir) }
        let fake = FakeProxy()
        let first = SystemProxyController(directory: dir, runner: fake.run)
        let second = SystemProxyController(directory: dir, runner: fake.run)
        XCTAssertThrowsError(try second.enable(host: "127.0.0.1", port: 1080))
        XCTAssertEqual(fake.writes, 0)
        withExtendedLifetime(first) { }
    }

    @MainActor
    private func appliedJournal(_ dir: URL, fake: FakeProxy) throws {
        fake.keys = ["Wi-Fi": .init(host: nil, port: nil, enabled: nil, httpEnabled: 1), "Ethernet": .init(host: "old.example", port: 1088, enabled: 1)]
        var first: SystemProxyController? = SystemProxyController(directory: dir, runner: fake.run, snapshotter: { fake.keys![$0]! }, effectiveChecker: { _, _ in true })
        try first!.enable(host: "127.0.0.1", port: 1080)
        first = nil
    }

    @MainActor
    func testResumeOwnAppliedJournalOnlyReadsAndRetainsRecoveryFile() async throws {
        let dir = try directory(); defer { try? FileManager.default.removeItem(at: dir) }
        let fake = FakeProxy(); try appliedJournal(dir, fake: fake)
        let path = dir.appendingPathComponent("system-proxy-restore.json")
        let saved = try Data(contentsOf: path), writes = fake.writes
        let resumed = SystemProxyController(directory: dir, runner: fake.run, snapshotter: { fake.keys![$0]! }, effectiveChecker: { $0 == "127.0.0.1" && $1 == 1080 })
        let healthy = await resumed.resumeOwnAppliedJournalAsync(host: "127.0.0.1", port: 1080)
        XCTAssertTrue(healthy); XCTAssertEqual(fake.writes, writes); XCTAssertEqual(try Data(contentsOf: path), saved)
        await MainActor.run { XCTAssertTrue(resumed.enabled); XCTAssertFalse(resumed.needsRecovery); XCTAssertFalse(resumed.changedExternally) }
        let stillApplied = await resumed.verifyStillAppliedAsync(); XCTAssertTrue(stillApplied)
    }

    @MainActor
    func testResumeRejectsForeignEndpointWithoutWrites() async throws {
        for endpoint in [("127.0.0.1", 1081), ("::1", 1080), ("203.0.113.1", 1080)] {
            let dir = try directory(); defer { try? FileManager.default.removeItem(at: dir) }
            let fake = FakeProxy(); try appliedJournal(dir, fake: fake); let writes = fake.writes
            let resumed = SystemProxyController(directory: dir, runner: fake.run, snapshotter: { fake.keys![$0]! }, effectiveChecker: { _, _ in true })
            let healthy = await resumed.resumeOwnAppliedJournalAsync(host: endpoint.0, port: endpoint.1)
            XCTAssertFalse(healthy); XCTAssertEqual(fake.writes, writes)
            await MainActor.run { XCTAssertFalse(resumed.enabled); XCTAssertTrue(resumed.needsRecovery) }
        }
    }

    @MainActor
    func testResumeRejectsExternalServiceChangesWithoutWrites() async throws {
        for field in ["socks", "http", "disabled", "missing"] {
            let dir = try directory(); defer { try? FileManager.default.removeItem(at: dir) }
            let fake = FakeProxy(); try appliedJournal(dir, fake: fake)
            if field == "socks" { fake.services["Wi-Fi"]!.host = "external.example"; fake.keys!["Wi-Fi"]!.host = "external.example" }
            if field == "http" { fake.keys!["Wi-Fi"]!.httpEnabled = 1 }
            if field == "disabled" { fake.services["Wi-Fi"]!.enabled = false; fake.keys!["Wi-Fi"]!.enabled = 0 }
            if field == "missing" { fake.services.removeValue(forKey: "Wi-Fi") }
            let writes = fake.writes, current = fake.services, keys = fake.keys
            let resumed = SystemProxyController(directory: dir, runner: fake.run, snapshotter: { fake.keys![$0]! }, effectiveChecker: { _, _ in true })
            let healthy = await resumed.resumeOwnAppliedJournalAsync(host: "127.0.0.1", port: 1080)
            XCTAssertFalse(healthy); XCTAssertEqual(fake.writes, writes); XCTAssertEqual(fake.services, current); XCTAssertEqual(fake.keys, keys)
            await MainActor.run { XCTAssertFalse(resumed.enabled); XCTAssertTrue(resumed.needsRecovery); XCTAssertTrue(resumed.changedExternally) }
        }
    }

    @MainActor
    func testResumeRejectsEffectiveProxyDriftAndInterruptedRestore() async throws {
        for interrupted in [false, true] {
            let dir = try directory(); defer { try? FileManager.default.removeItem(at: dir) }
            let fake = FakeProxy(); try appliedJournal(dir, fake: fake)
            if interrupted {
                let path = dir.appendingPathComponent("system-proxy-restore.json")
                var journal = try JSONSerialization.jsonObject(with: Data(contentsOf: path)) as! [String: Any]
                journal["restoring"] = ["Wi-Fi"]
                try JSONSerialization.data(withJSONObject: journal).write(to: path)
                try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: path.path)
            }
            let writes = fake.writes
            let resumed = SystemProxyController(directory: dir, runner: fake.run, snapshotter: { fake.keys![$0]! }, effectiveChecker: { _, _ in interrupted })
            let healthy = await resumed.resumeOwnAppliedJournalAsync(host: "127.0.0.1", port: 1080)
            XCTAssertFalse(healthy); XCTAssertEqual(fake.writes, writes)
            await MainActor.run { XCTAssertFalse(resumed.enabled); XCTAssertTrue(resumed.needsRecovery) }
        }
    }

    @MainActor
    func testSecondAppCannotResumeOrRestoreJournalWithoutLock() async throws {
        let dir = try directory(); defer { try? FileManager.default.removeItem(at: dir) }
        let fake = FakeProxy(); try appliedJournal(dir, fake: fake)
        let first = SystemProxyController(directory: dir, runner: fake.run, snapshotter: { fake.keys![$0]! }, effectiveChecker: { _, _ in true })
        let second = SystemProxyController(directory: dir, runner: fake.run, snapshotter: { fake.keys![$0]! }, effectiveChecker: { _, _ in true })
        let writes = fake.writes
        let healthy = await second.resumeOwnAppliedJournalAsync(host: "127.0.0.1", port: 1080)
        XCTAssertFalse(healthy); XCTAssertThrowsError(try second.restore()); XCTAssertEqual(fake.writes, writes)
        withExtendedLifetime(first) { }
    }
}
