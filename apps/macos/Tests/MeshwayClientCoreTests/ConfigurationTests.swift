import Foundation
import XCTest
@testable import MeshwayClientCore

final class ConfigurationTests: XCTestCase {
    func testDefaultIsPrivateAndUsesSecureToken() throws {
        let config = try Configuration.makeDefault(stateDirectory: "/tmp/meshway-test")
        XCTAssertEqual(config.apiToken.count, 64)
        XCTAssertFalse(config.exit)
        XCTAssertFalse(config.relay)
        XCTAssertFalse(config.allowPrivateTargets)
        XCTAssertEqual(try config.apiBaseURL().host, "127.0.0.1")
        XCTAssertNotEqual(config.apiToken, try Configuration.makeDefault(stateDirectory: "/tmp/meshway-test").apiToken)
    }

    func testRejectsRemoteAndMalformedControlEndpoints() throws {
        let invalid = ["0.0.0.0:7777", "example.com:7777", "127.0.0.1:0", "127.0.0.1:65536", "user@127.0.0.1:7777", "127.0.0.1:7777/path", "127.0.0.1:7777?x=y"]
        for value in invalid {
            var config = try Configuration.makeDefault(stateDirectory: "/tmp/meshway-test")
            config.apiListen = value
            XCTAssertThrowsError(try config.validate(), value)
        }
    }

    func testAcceptsIPv6Loopback() throws {
        var config = try Configuration.makeDefault(stateDirectory: "/tmp/meshway-test")
        config.apiListen = "[::1]:7777"
        config.socksListen = ""
        XCTAssertNoThrow(try config.validate())
    }

    func testRequiresExitForPrivateForwarding() throws {
        var config = try Configuration.makeDefault(stateDirectory: "/tmp/meshway-test")
        config.allowPrivateTargets = true
        XCTAssertThrowsError(try config.validate())
        config.exit = true
        XCTAssertNoThrow(try config.validate())
    }

    func testEditsPreserveUnknownAndForwardFields() throws {
        var object = try JSONSerialization.jsonObject(with: Configuration.makeDefault(stateDirectory: "/tmp/meshway-test").encoded()) as! [String: Any]
        object["future_field"] = ["enabled": true]
        object["forwards"] = [["name": "test", "listen": "127.0.0.1:8080", "target": "example.com:80"]]
        var config = try Configuration(data: JSONSerialization.data(withJSONObject: object))
        config.relay = true
        let saved = try JSONSerialization.jsonObject(with: config.encoded()) as! [String: Any]
        XCTAssertEqual(saved["future_field"] as? [String: Bool], ["enabled": true])
        XCTAssertEqual((saved["forwards"] as? [[String: String]])?.first?["name"], "test")
        XCTAssertEqual(saved["relay"] as? Bool, true)
    }

    func testSavedConfigHasOwnerOnlyPermissions() throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: directory) }
        let path = directory.appendingPathComponent("config.json")
        let config = try Configuration.makeDefault(stateDirectory: directory.appendingPathComponent("node").path)
        try config.save(to: path)
        let attributes = try FileManager.default.attributesOfItem(atPath: path.path)
        XCTAssertEqual((attributes[.posixPermissions] as? NSNumber)?.intValue, 0o600)
    }
}
